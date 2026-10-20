package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/references"
)

// Multipart edit/video uploads use the existing durable reference library. Their
// URLs, rather than base64 file bodies, are persisted in queued jobs.
func (s *Server) multipartGeneration(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, bool) {
	if s.references == nil || s.mediaSigner == nil {
		writeError(w, 503, "service_unavailable", "reference storage is unavailable")
		return nil, false
	}
	if r.URL.Path != "/v1/images/edits" && r.URL.Path != "/v1/videos" {
		invalidParameter(w, "body", "multipart is supported for image edits and video input references")
		return nil, false
	}
	if direct, err := s.references.RequiresDirectUpload(r.Context()); err != nil {
		s.referenceError(w, err)
		return nil, false
	} else if direct {
		s.referenceError(w, references.ErrDirectUploadRequired)
		return nil, false
	}
	release, err := s.references.AdmitMultipart()
	if err != nil {
		s.referenceError(w, err)
		return nil, false
	}
	defer release()
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(2 * time.Minute))
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(3 * time.Minute))
	// Total upload budget, independent of the number of files. The library applies
	// its per-file MIME and size checks, storage quota and owner isolation too.
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		invalidParameter(w, "body", "invalid multipart request or upload exceeds 32 MiB")
		return nil, false
	}
	defer r.MultipartForm.RemoveAll()
	fields := map[string]json.RawMessage{}
	for key, values := range r.MultipartForm.Value {
		if len(values) != 1 {
			invalidParameter(w, key, "field must appear once")
			return nil, false
		}
		value := values[0]
		if len(value) > 64<<10 {
			invalidParameter(w, key, "field exceeds 64 KiB")
			return nil, false
		}
		switch key {
		case "options", "n", "stream", "max_expected_cost_usd", "timeout_seconds":
			fields[key] = json.RawMessage(value)
		default:
			fields[key], _ = json.Marshal(value)
		}
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
	if _, param, err := s.openAIInput(r, probe); err != nil {
		invalidParameter(w, param, err.Error())
		return nil, false
	}
	urls := []string{}
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			invalidParameter(w, key, "could not read uploaded file")
			return nil, false
		}
		// Sniff before persistence; image-edit inputs must not store an audio/video
		// file under an image parameter simply because its filename says .png.
		prefix := make([]byte, 512)
		n, _ := file.Read(prefix)
		_, _ = file.Seek(0, 0)
		if !strings.HasPrefix(http.DetectContentType(prefix[:n]), "image/") {
			_ = file.Close()
			invalidParameter(w, key, "input reference must be an image")
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
			item, err := s.references.Get(r.Context(), one)
			if err != nil {
				return nil, errors.New("reference file is not available to this API key")
			}
			if !strings.HasPrefix(item.ContentType, "image/") {
				return nil, errors.New("reference file must be an image")
			}
			return []string{s.referenceAbsoluteURL(r, item)}, nil
		}
	}
	return nil, fmt.Errorf("use an image URL, {image_url: URL}, {file_id: reference ID}, or an array of these")
}
