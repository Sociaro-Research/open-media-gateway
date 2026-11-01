package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
)

// Multipart edit/video uploads use the existing durable reference library. Their
// URLs, rather than base64 file bodies, are persisted in queued jobs.
func (s *Server) multipartGeneration(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, bool) {
	if r.URL.Path != "/v1/images/edits" && r.URL.Path != "/v1/videos" {
		invalidParameter(w, "body", "multipart is supported for image edits and videos")
		return nil, false
	}
	// Bound parsing and disk spooling before consuming the request body.
	if s.references != nil {
		release, err := s.references.AdmitMultipart()
		if err != nil {
			s.referenceError(w, err)
			return nil, false
		}
		defer release()
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(2 * time.Minute))
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(3 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	if r.URL.Path == "/v1/videos" {
		fields, files, ok := videoMultipartFields(w, r)
		if !ok {
			return nil, false
		}
		if !files {
			return fields, true
		}
	}
	if s.references == nil || s.mediaSigner == nil {
		writeError(w, 503, "service_unavailable", "reference storage is unavailable")
		return nil, false
	}
	if direct, err := s.references.RequiresDirectUpload(r.Context()); err != nil {
		s.referenceError(w, err)
		return nil, false
	} else if direct {
		s.referenceError(w, references.ErrDirectUploadRequired)
		return nil, false
	}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		invalidParameter(w, "body", "invalid multipart request or upload exceeds ≈33.55 MB (33554432 bytes)")
		return nil, false
	}
	defer r.MultipartForm.RemoveAll()
	fields, ok := multipartGenerationFields(w, r.MultipartForm.Value)
	if !ok {
		return nil, false
	}
	key := "image"
	if r.URL.Path == "/v1/videos" {
		key = "input_reference"
	}
	files := r.MultipartForm.File[key]
	if len(r.MultipartForm.File[key+"[]"]) > 0 {
		files = append(files, r.MultipartForm.File[key+"[]"]...)
	}
	for name := range r.MultipartForm.File {
		if name != key && name != key+"[]" {
			invalidParameter(w, name, "unsupported file field")
			return nil, false
		}
	}
	if len(files) == 0 || len(files) > 16 || (key == "input_reference" && len(files) != 1) {
		invalidParameter(w, key, "send 1–16 images for an edit, or one video input reference")
		return nil, false
	}
	if fields[key] != nil {
		invalidParameter(w, key, "send files or URLs, not both")
		return nil, false
	}
	// Validate names/model/options before storing uploads. Core admission still
	// validates model-specific capabilities and budget when accepting the job.
	probe := map[string]json.RawMessage{}
	for k, v := range fields {
		probe[k] = v
	}
	probe[key] = json.RawMessage(`"https://reference.invalid/upload"`)
	input, param, err := s.openAIInput(r, probe)
	if err != nil {
		invalidParameter(w, param, err.Error())
		return nil, false
	}
	// Inspect the whole bounded multipart batch before retaining its first file.
	inspected := []referencevalidation.File{}
	for _, header := range files {
		file, e := header.Open()
		if e != nil {
			invalidParameter(w, key, "Could not read uploaded file.")
			return nil, false
		}
		data, e := io.ReadAll(io.LimitReader(file, 32<<20))
		file.Close()
		if e != nil {
			invalidParameter(w, key, "Could not read uploaded file.")
			return nil, false
		}
		if detected := http.DetectContentType(data); !strings.HasPrefix(detected, "image/") {
			invalidParameter(w, key, header.Filename+": format "+detected+"; input reference must be an image")
			return nil, false
		}
		media, e := references.InspectFile(header.Filename, data, "")
		if e != nil {
			s.referenceError(w, e)
			return nil, false
		}
		if media.Type != "image" {
			invalidParameter(w, key, "Input reference must be an image.")
			return nil, false
		}
		inspected = append(inspected, media)
	}
	if err = s.referenceValidator(r)(r.Context(), &referencevalidation.Target{Provider: input.Provider, Model: input.Model, Options: input.Options}, inspected, false); err != nil {
		s.referenceError(w, err)
		return nil, false
	}
	urls := []string{}
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			invalidParameter(w, key, "could not read uploaded file")
			return nil, false
		}
		item, err := s.references.Upload(r.Context(), header.Filename, file)
		_ = file.Close()
		if err != nil {
			s.referenceError(w, err)
			return nil, false
		}
		urls = append(urls, s.referenceAbsoluteURL(r, item))
	}
	fields[key], _ = json.Marshal(urls)
	return fields, true
}

// videoMultipartFields accepts scalar-only video forms without involving upload
// storage. On the first file header, replay the bounded prefix so the existing
// upload parser still runs only after storage/direct-upload admission. Multipart
// readers may prefetch a small buffer, but no file is drained or spooled here.
func videoMultipartFields(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, bool, bool) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		invalidParameter(w, "body", "invalid multipart request")
		return nil, false, false
	}
	var prefix bytes.Buffer
	reader := multipart.NewReader(io.TeeReader(r.Body, &prefix), params["boundary"])
	values := map[string][]string{}
	for parts := 0; ; parts++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			fields, ok := multipartGenerationFields(w, values)
			return fields, false, ok
		}
		if err != nil {
			invalidParameter(w, "body", "invalid multipart request or upload exceeds ≈33.55 MB (33554432 bytes)")
			return nil, false, false
		}
		if parts >= 1000 {
			invalidParameter(w, "body", "multipart form exceeds 1000 parts")
			return nil, false, false
		}
		if part.FileName() != "" {
			r.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(prefix.Bytes()), r.Body), r.Body}
			return nil, true, true
		}
		name := part.FormName()
		value, err := io.ReadAll(io.LimitReader(part, (64<<10)+1))
		if err != nil || len(value) > 64<<10 {
			invalidParameter(w, name, "field exceeds ≈65.54 KB (65536 bytes) or could not be read")
			return nil, false, false
		}
		// Match the standard form parser's bounded in-memory field budget.
		if prefix.Len() > 11<<20 {
			invalidParameter(w, "body", "multipart fields exceed ≈11.53 MB (11534336 bytes)")
			return nil, false, false
		}
		values[name] = append(values[name], string(value))
	}
}

func multipartGenerationFields(w http.ResponseWriter, values map[string][]string) (map[string]json.RawMessage, bool) {
	fields := map[string]json.RawMessage{}
	for key, values := range values {
		if len(values) != 1 {
			invalidParameter(w, key, "field must appear once")
			return nil, false
		}
		value := values[0]
		if len(value) > 64<<10 {
			invalidParameter(w, key, "field exceeds ≈65.54 KB (65536 bytes)")
			return nil, false
		}
		switch key {
		case "options", "n", "stream", "max_expected_cost_usd", "timeout_seconds":
			fields[key] = json.RawMessage(value)
		default:
			fields[key], _ = json.Marshal(value)
		}
	}
	return fields, true
}

func (s *Server) referenceAbsoluteURL(r *http.Request, item references.Image) string {
	// Generated capability paths are resolved from owner-scoped storage by the
	// provider manager. HTTPS is the public deployment's transport.
	return "https://" + r.Host + s.referenceView(r, item).URL
}

func (s *Server) inputReferenceURLs(r *http.Request, raw json.RawMessage) ([]string, error) {
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return []string{one}, nil
	}
	var array []json.RawMessage
	if json.Unmarshal(raw, &array) == nil && len(array) > 0 {
		var urls []string
		for _, item := range array {
			if strings.HasPrefix(strings.TrimSpace(string(item)), "[") {
				return nil, errors.New("reference arrays must contain only image URLs or reference objects")
			}
			values, err := s.inputReferenceURLs(r, item)
			if err != nil {
				return nil, err
			}
			urls = append(urls, values...)
		}
		return urls, nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil && len(object) == 1 {
		if value, ok := object["image_url"]; ok {
			if json.Unmarshal(value, &one) == nil && one != "" {
				return []string{one}, nil
			}
		}
		if value, ok := object["file_id"]; ok && s.references != nil && s.mediaSigner != nil {
			if json.Unmarshal(value, &one) != nil {
				return nil, errors.New("file_id must be a string")
			}
			item, refContext, err := s.generationReference(r.Context(), one)
			if err != nil {
				return nil, errors.New("reference file is not available to this API key")
			}
			if !strings.HasPrefix(item.ContentType, "image/") {
				return nil, errors.New("reference file must be an image")
			}
			return []string{s.referenceAbsoluteURL(r.WithContext(refContext), item)}, nil
		}
	}
	return nil, fmt.Errorf("use an image URL, {image_url: URL}, {file_id: reference ID}, or an array of these")
}
