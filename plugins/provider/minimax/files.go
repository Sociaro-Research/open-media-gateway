package minimax

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type File struct {
	ID          json.Number `json:"file_id"`
	Bytes       int64       `json:"bytes"`
	CreatedAt   int64       `json:"created_at"`
	Filename    string      `json:"filename"`
	Purpose     string      `json:"purpose"`
	DownloadURL string      `json:"download_url,omitempty"`
	Handle      string      `json:"-"`
	URI         string      `json:"-"`
	MediaKind   string      `json:"-"`
}

func (a *Adapter) retrieveFile(ctx context.Context, id string) (File, error) {
	if !numericID.MatchString(id) {
		return File{}, errors.New("invalid MiniMax file ID")
	}
	var out struct {
		File File `json:"file"`
	}
	if e := a.json(ctx, "GET", "/v1/files/retrieve?file_id="+id, nil, &out); e != nil {
		return File{}, e
	}
	if out.File.ID.String() != id || out.File.Bytes < 0 || out.File.Purpose == "" {
		return File{}, errors.New("MiniMax file response does not match the requested resource")
	}
	return out.File, nil
}
func (a *Adapter) fileView(ctx context.Context, f File) File {
	h := handle{ID: f.ID.String(), Kind: "file", Purpose: f.Purpose, MediaKind: f.MediaKind}
	if f.Purpose == "video_generation_input" {
		h.ExpiresAt = time.Now().Add(7 * 24 * time.Hour).Unix()
		if f.CreatedAt > 0 {
			h.ExpiresAt = f.CreatedAt + 7*86400
		}
	}
	f.Handle = a.seal(ctx, h)
	if referenceFilePurpose(h, h.MediaKind) {
		f.URI = "asset://" + base64.RawURLEncoding.EncodeToString([]byte(f.Handle))
	}
	return f
}

// Translate equivalent inspected MIME labels to MiniMax's documented upload
// spelling; this never replaces trusted byte inspection or size/hash checks.
func uploadContentType(raw string) (string, error) {
	typ, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return "", err
	}
	switch typ {
	case "audio/wave", "audio/x-wav":
		typ = "audio/wav"
	case "image/heif":
		typ = "image/heic"
	}
	return typ, nil
}

func (a *Adapter) UploadFile(ctx context.Context, filename, contentType string, reader io.Reader) (File, error) {
	if a.requestConnectionScope(ctx) == "" {
		return File{}, errors.New("MiniMax provider connection identity is not bound")
	}
	typ, e := uploadContentType(contentType)
	if e != nil {
		return File{}, invalid("invalid file content type")
	}
	limit := map[string]int{"image/jpeg": 30_000_000, "image/png": 30_000_000, "image/webp": 30_000_000, "image/heic": 30_000_000, "image/heif": 30_000_000, "video/mp4": 50_000_000, "video/quicktime": 50_000_000, "audio/wav": 15_000_000, "audio/mpeg": 15_000_000}[typ]
	ext := strings.ToLower(filepath.Ext(filename))
	extensions := map[string][]string{"image/jpeg": {".jpg", ".jpeg"}, "image/png": {".png"}, "image/webp": {".webp"}, "image/heic": {".heic", ".heif"}, "image/heif": {".heif"}, "video/mp4": {".mp4"}, "video/quicktime": {".mov"}, "audio/wav": {".wav"}, "audio/mpeg": {".mp3"}}
	if limit == 0 || !slices.Contains(extensions[typ], ext) || filepath.Base(filename) != filename || strings.ContainsAny(filename, "\r\n") || len(filename) > 200 {
		return File{}, invalid("unsupported upload type or filename")
	}
	b, e := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if e != nil || len(b) == 0 || len(b) > limit {
		return File{}, invalid("file is empty, unreadable or exceeds its per-media limit")
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if e = w.WriteField("purpose", "video_generation_input"); e != nil {
		return File{}, e
	}
	part, e := w.CreateFormFile("file", filename)
	if e != nil {
		return File{}, e
	}
	if _, e = part.Write(b); e != nil {
		return File{}, e
	}
	if e = w.Close(); e != nil {
		return File{}, e
	}
	raw, e := a.do(ctx, "POST", "/v1/files/upload", &body, w.FormDataContentType())
	if e != nil {
		return File{}, e
	}
	var out struct {
		File File `json:"file"`
	}
	if json.Unmarshal(raw, &out) != nil || !numericID.MatchString(out.File.ID.String()) || out.File.Purpose != "video_generation_input" || out.File.Bytes != int64(len(b)) {
		return File{}, errors.New("MiniMax upload returned an invalid file; outcome may be unknown")
	}
	out.File.MediaKind = strings.SplitN(typ, "/", 2)[0]
	return a.fileView(ctx, out.File), nil
}
func (a *Adapter) GetFile(ctx context.Context, token string) (File, error) {
	h, e := a.open(ctx, token)
	if e != nil {
		return File{}, e
	}
	if h.Kind != "file" {
		return File{}, invalid("not a file handle")
	}
	f, e := a.retrieveFile(ctx, h.ID)
	if e != nil {
		return File{}, e
	}
	if f.Purpose != h.Purpose {
		return File{}, errors.New("MiniMax file purpose changed")
	}
	f.MediaKind = h.MediaKind
	f.Handle = token
	if referenceFilePurpose(h, h.MediaKind) {
		f.URI = "asset://" + base64.RawURLEncoding.EncodeToString([]byte(token))
	}
	return f, nil
}
func (a *Adapter) ListFiles(ctx context.Context, tokens []string) ([]File, error) {
	if len(tokens) == 0 || len(tokens) > 100 {
		return nil, invalid("file listing requires 1–100 owner-scoped file handles")
	}
	allowed := map[string]string{}
	for _, token := range tokens {
		h, e := a.open(ctx, token)
		if e != nil {
			return nil, e
		}
		if h.Kind != "file" || h.Purpose != "video_generation_input" {
			return nil, invalid("only video input files can be listed")
		}
		allowed[h.ID] = token
	}
	var out struct {
		Files []File `json:"files"`
	}
	if e := a.json(ctx, "GET", "/v1/files/list?purpose=video_generation_input", nil, &out); e != nil {
		return nil, e
	}
	result := []File{}
	for _, f := range out.Files {
		if _, ok := allowed[f.ID.String()]; !ok {
			continue
		}
		if f.Purpose != "video_generation_input" || f.Bytes < 0 {
			return nil, errors.New("MiniMax file listing returned an invalid file")
		}
		h, _ := a.open(ctx, allowed[f.ID.String()])
		f.MediaKind = h.MediaKind
		f.Handle = allowed[f.ID.String()]
		f.URI = "asset://" + base64.RawURLEncoding.EncodeToString([]byte(f.Handle))
		result = append(result, f)
	}
	return result, nil
}

// DownloadFile retrieves bounded bytes through the authenticated content API;
// provider response bodies never receive credentials from another account.
func (a *Adapter) DownloadFile(ctx context.Context, token string, maxBytes int64) ([]byte, error) {
	reader, _, _, e := a.openFile(ctx, token, maxBytes)
	if e != nil {
		return nil, e
	}
	defer reader.Close()
	b, e := io.ReadAll(reader)
	if e != nil {
		return nil, e
	}
	return b, nil
}

// OpenArtifact is a trusted streaming alternative for the separately documented
// authenticated file-content API. It returns no URL carrying bearer credentials;
// shared archival must invoke it only with this owner/connection-bound handle.
func (a *Adapter) OpenArtifact(ctx context.Context, token string) (core.ArtifactSource, error) {
	reader, typ, size, err := a.openFile(ctx, token, 200_000_000)
	if err != nil {
		return core.ArtifactSource{}, err
	}
	return core.ArtifactSource{Reader: reader, ContentType: typ, SizeBytes: size}, nil
}

var _ core.ArtifactOpener = (*Adapter)(nil)

func (a *Adapter) openFile(ctx context.Context, token string, maxBytes int64) (io.ReadCloser, string, int64, error) {
	h, e := a.open(ctx, token)
	if e != nil {
		return nil, "", 0, e
	}
	if h.Kind != "file" || maxBytes <= 0 || maxBytes > 200_000_000 {
		return nil, "", 0, invalid("invalid file handle or download limit")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", a.base+"/v1/files/retrieve_content?file_id="+h.ID, nil)
	if e != nil {
		return nil, "", 0, e
	}
	req.Header.Set("Authorization", "Bearer "+a.key)
	req.Header.Set("User-Agent", "OpenMediaGateway-MiniMax/1.0")
	response, e := httpclient.Do(a.client, req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, "", 0, ctx.Err()
		}
		return nil, "", 0, errors.New("MiniMax file download transport failed")
	}
	fail := func(err error) (io.ReadCloser, string, int64, error) { response.Body.Close(); return nil, "", 0, err }
	if response.StatusCode != 200 {
		return fail(&APIError{Status: response.StatusCode})
	}
	if response.ContentLength > maxBytes {
		return fail(errors.New("MiniMax file download exceeds the limit"))
	}
	buffered := bufio.NewReaderSize(response.Body, 512)
	head, e := buffered.Peek(512)
	if e != nil && e != io.EOF {
		return fail(errors.New("MiniMax file content cannot be read"))
	}
	if len(head) == 0 {
		return fail(errors.New("MiniMax file content is empty"))
	}
	// The official schema labels binary application/json; signatures prevent a
	// JSON error document from being archived as media despite that source label.
	detected := sniffMedia(head)
	if !strings.HasPrefix(detected, "image/") && !strings.HasPrefix(detected, "video/") && !strings.HasPrefix(detected, "audio/") {
		return fail(errors.New("MiniMax file download has no supported media signature"))
	}
	if h.MediaKind != "" && !strings.HasPrefix(detected, h.MediaKind+"/") {
		return fail(errors.New("MiniMax file content does not match its owned media kind"))
	}
	length := response.ContentLength
	if length <= 0 {
		length = -1
	}
	return &boundedFileReader{reader: buffered, closer: response.Body, remaining: maxBytes}, detected, length, nil
}

type boundedFileReader struct {
	reader    io.Reader
	closer    io.Closer
	remaining int64
}

func (r *boundedFileReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		var extra [1]byte
		n, e := r.reader.Read(extra[:])
		if n > 0 {
			return 0, errors.New("MiniMax file download exceeds the limit")
		}
		return 0, e
	}
	if int64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	n, e := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, e
}
func (r *boundedFileReader) Close() error { return r.closer.Close() }

// DeleteFile follows the published delete-purpose enum exactly. The upload API's
// video_generation_input purpose is absent from that enum; deleting input files
// remains unavailable until MiniMax publishes a compatible deletion contract.
func (a *Adapter) DeleteFile(ctx context.Context, token string) error {
	h, e := a.open(ctx, token)
	if e != nil {
		return e
	}
	if h.Kind != "file" || h.Purpose != "video_generation" {
		return invalid("MiniMax does not document deletion of video_generation_input files; they expire after seven days")
	}
	var out struct {
		ID json.Number `json:"file_id"`
	}
	if e = a.json(ctx, "POST", "/v1/files/delete", map[string]any{"file_id": json.Number(h.ID), "purpose": h.Purpose}, &out); e != nil {
		return e
	}
	if out.ID.String() != h.ID {
		return errors.New("MiniMax file deletion result is malformed; outcome is uncertain")
	}
	return nil
}

// A generated file capability can only be minted from an owned successful
// generation, and remains restricted to video. Raw platform IDs are never inputs.
func referenceFilePurpose(h handle, kind string) bool {
	return h.Purpose == "video_generation_input" || h.Purpose == "video_generation" && kind == "video" && h.MediaKind == "video"
}

func (a *Adapter) resolveFileInput(ctx context.Context, value, expectedKind string) (string, error) {
	if !strings.HasPrefix(value, "asset://") {
		return value, nil
	}
	raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "asset://"))
	if e != nil {
		return "", invalid("invalid MiniMax file reference")
	}
	h, e := a.open(ctx, string(raw))
	if e != nil {
		return "", e
	}
	if h.Kind != "file" || !referenceFilePurpose(h, expectedKind) || h.MediaKind != expectedKind {
		return "", invalid("only owned uploaded inputs or generated video files can be referenced")
	}
	return "mm_file://" + h.ID, nil
}

// GetTaskFile mints an output-file capability only after querying an owned,
// successful legacy generation. Raw provider file IDs are never public inputs.
func (a *Adapter) GetTaskFile(ctx context.Context, token string) (File, error) {
	h, e := a.open(ctx, token)
	if e != nil {
		return File{}, e
	}
	if h.Kind != "legacy" {
		return File{}, invalid("this task type has no documented output file ID")
	}
	var out struct {
		ID     string `json:"task_id"`
		Status string `json:"status"`
		File   string `json:"file_id"`
	}
	if e = a.json(ctx, "GET", "/v1/query/video_generation?task_id="+h.ID, nil, &out); e != nil {
		return File{}, e
	}
	if out.ID != h.ID || out.Status != "Success" || !numericID.MatchString(out.File) {
		return File{}, invalid("an owned successful video task is required")
	}
	f, e := a.retrieveFile(ctx, out.File)
	if e != nil {
		return File{}, e
	}
	if f.Purpose != "video_generation" {
		return File{}, errors.New("MiniMax output file has an unexpected purpose")
	}
	f.MediaKind = "video"
	return a.fileView(ctx, f), nil
}

// net/http does not identify every documented MiniMax ISO-BMFF image/MOV brand.
// Recognize only exact file-type brands; an unrecognized binary stays rejected.
func sniffMedia(head []byte) string {
	detected := http.DetectContentType(head)
	if strings.HasPrefix(detected, "image/") || strings.HasPrefix(detected, "video/") || strings.HasPrefix(detected, "audio/") {
		return detected
	}
	if len(head) < 16 || string(head[4:8]) != "ftyp" {
		return detected
	}
	size := uint64(binary.BigEndian.Uint32(head[:4]))
	start := 8
	if size == 1 {
		if len(head) < 24 {
			return detected
		}
		size = binary.BigEndian.Uint64(head[8:16])
		start = 16
	}
	if size < uint64(start+8) {
		return detected
	}
	end := min(len(head), int(min(size, uint64(512))))
	brands := map[string]bool{}
	for i := start; i+4 <= end; i += 4 {
		if i == start+4 {
			continue
		}
		brands[string(head[i:i+4])] = true
	}
	for _, b := range []string{"heic", "heix", "hevc", "hevx"} {
		if brands[b] {
			return "image/heic"
		}
	}
	if brands["mif1"] || brands["msf1"] {
		return "image/heif"
	}
	if brands["qt  "] {
		return "video/quicktime"
	}
	for b := range brands {
		if b == "isom" || b == "iso2" || b == "avc1" || strings.HasPrefix(b, "mp4") {
			return "video/mp4"
		}
	}
	return detected
}
