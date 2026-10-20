package api

// The public generation contract uses OpenAI field names. Admission, billing,
// isolation and execution share one asynchronous core.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
)

func isModelCatalogPath(path string) bool {
	return path == "/v1/providers" || path == "/v1/models" || strings.HasPrefix(path, "/v1/models/")
}

func isGenerationPath(path string) bool {
	switch path {
	case "/v1/videos", "/v1/images/generations", "/v1/images/edits", "/v1/audio/speech", "/v1/audio/generations", "/v1/generations", "/v1/generations/estimate":
		return true
	}
	return false
}

func (s *Server) registerOpenAIRoutes(mux *http.ServeMux) {
	mux.Handle("POST /v1/videos", s.authenticate(http.HandlerFunc(s.createOpenAIGeneration)))
	mux.Handle("POST /v1/images/generations", s.authenticate(http.HandlerFunc(s.createOpenAIGeneration)))
	mux.Handle("POST /v1/images/edits", s.authenticate(http.HandlerFunc(s.createOpenAIGeneration)))
	mux.Handle("POST /v1/audio/speech", s.authenticate(http.HandlerFunc(s.createOpenAIGeneration)))
	mux.Handle("POST /v1/audio/generations", s.authenticate(http.HandlerFunc(s.createOpenAIGeneration)))
	mux.Handle("POST /v1/generations", s.authenticate(http.HandlerFunc(s.createOpenAIGeneration)))
	mux.Handle("POST /v1/generations/estimate", s.authenticate(http.HandlerFunc(s.createOpenAIGeneration)))
	mux.Handle("GET /v1/models", s.authenticate(http.HandlerFunc(s.listModels)))
	mux.Handle("GET /v1/models/{model...}", s.authenticate(http.HandlerFunc(s.getModel)))
	mux.Handle("GET /v1/generations/{id}/media-access", s.authenticate(http.HandlerFunc(s.mediaAccess)))
	mux.Handle("GET /v1/generations/{id}/last-frame", s.authenticate(http.HandlerFunc(s.serveAuthorizedLastFrame)))
	mux.Handle("HEAD /v1/generations/{id}/last-frame", s.authenticate(http.HandlerFunc(s.serveAuthorizedLastFrame)))
	mux.Handle("DELETE /v1/generations/{id}/content", s.authenticate(http.HandlerFunc(s.deleteResult)))
	mux.Handle("GET /v1/generations", s.authenticate(http.HandlerFunc(s.listGenerations)))
	mux.Handle("GET /v1/generations/{id}", s.authenticate(http.HandlerFunc(s.getGeneration)))
	mux.Handle("GET /v1/generations/{id}/content", s.authenticate(http.HandlerFunc(s.generationContent)))
	mux.Handle("HEAD /v1/generations/{id}/content", s.authenticate(http.HandlerFunc(s.generationContent)))
	mux.Handle("GET /v1/videos/{id}", s.authenticate(http.HandlerFunc(s.getGeneration)))
	mux.Handle("GET /v1/videos/{id}/content", s.authenticate(http.HandlerFunc(s.generationContent)))
	mux.Handle("HEAD /v1/videos/{id}/content", s.authenticate(http.HandlerFunc(s.generationContent)))
}

func writeAPIError(w http.ResponseWriter, status int, code, message string, param *string) {
	typ := "invalid_request_error"
	switch {
	case status == 401:
		typ = "authentication_error"
	case status == 403:
		typ = "permission_error"
	case status == 429:
		typ = "rate_limit_error"
	case status >= 500:
		typ = "server_error"
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"type": typ, "code": code, "message": message, "param": param}})
}

func invalidParameter(w http.ResponseWriter, name, message string) {
	writeAPIError(w, 400, "invalid_request", message, &name)
}

func (s *Server) visibleCatalog(r *http.Request) []core.ProviderDescriptor {
	catalog, ok := r.Context().Value(modelCatalogKey{}).([]core.ProviderDescriptor)
	if !ok {
		catalog = s.service.ListProviders(r.Context())
	}
	return catalog
}

type modelResponse struct {
	ID       string `json:"id"`
	Object   string `json:"object"`
	Created  *int64 `json:"created"`
	OwnedBy  string `json:"owned_by"`
	Provider string `json:"provider"`
	// Capabilities also carry the existing role-filtered pricing and provenance.
	Capabilities core.MediaModel `json:"capabilities"`
}

func catalogModels(catalog []core.ProviderDescriptor) []modelResponse {
	result := []modelResponse{}
	for _, p := range catalog {
		for _, m := range p.Models {
			id := m.PublicID
			if id == "" {
				id = p.ID + "/" + m.ID
			}
			v := modelResponse{ID: id, Object: "model", OwnedBy: p.ID, Provider: p.ID, Capabilities: m}
			if m.Metadata != nil {
				if m.Metadata.APICreated > 0 {
					value := m.Metadata.APICreated
					v.Created = &value
				}
				if m.Metadata.OwnedBy != "" {
					v.OwnedBy = m.Metadata.OwnedBy
				}
			}
			result = append(result, v)
		}
	}
	return result
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	s.writeViewerJSON(w, r, 200, map[string]any{"object": "list", "data": catalogModels(s.visibleCatalog(r))})
}

func (s *Server) getModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("model")
	for _, model := range catalogModels(s.visibleCatalog(r)) {
		if model.ID == id {
			s.writeViewerJSON(w, r, 200, model)
			return
		}
	}
	writeError(w, 404, "model_not_found", "model is not available to this API key")
}

func (s *Server) generationTask(w http.ResponseWriter, r *http.Request) (core.Task, bool) {
	task, err := s.service.GetTask(r.Context(), r.PathValue("id"))
	if errors.Is(err, core.ErrTaskNotFound) || (err == nil && strings.HasPrefix(r.URL.Path, "/v1/videos/") && task.MediaType != "video") {
		writeError(w, 404, "not_found", "generation not found")
		return core.Task{}, false
	}
	if err != nil {
		writeError(w, 503, "service_unavailable", "generation could not be read")
		return core.Task{}, false
	}
	role := identity.OrganizationFromContext(r.Context()).Role
	if role != "" && !org.Statistics(role) && task.ActorID != identity.ActorID(r.Context()) {
		writeError(w, 404, "not_found", "generation not found")
		return core.Task{}, false
	}
	return task, true
}

func (s *Server) getGeneration(w http.ResponseWriter, r *http.Request) {
	if task, ok := s.generationTask(w, r); ok {
		s.writeGenerationJSON(w, r, 200, task)
	}
}

func (s *Server) generationContent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.generationTask(w, r); ok {
		s.serveAuthorizedMedia(w, r)
	}
}

func (s *Server) listGenerations(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 1000000 {
			invalidParameter(w, "offset", "offset must be an integer from 0 to 1000000")
			return
		}
		offset = value
	}
	for key := range r.URL.Query() {
		if key != "limit" && key != "offset" {
			invalidParameter(w, key, "only limit and offset are supported; cursor pagination is not available")
			return
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			invalidParameter(w, "limit", "limit must be an integer from 1 to 100")
			return
		}
		limit = value
	}
	var tasks []core.Task
	var err error
	more := false
	if reader, ok := s.service.(interface {
		ListTasksPage(context.Context, int, int) ([]core.Task, error)
	}); ok {
		tasks, err = reader.ListTasksPage(r.Context(), limit+1, offset)
		if len(tasks) > limit {
			more = true
			tasks = tasks[:limit]
		}
	} else {
		tasks, err = s.service.ListTasks(r.Context(), limit)
		if offset > 0 {
			tasks = []core.Task{}
		}
	}
	if err != nil {
		writeError(w, 503, "service_unavailable", "generations could not be read")
		return
	}
	data := make([]any, 0, len(tasks))
	for _, t := range tasks {
		role := identity.OrganizationFromContext(r.Context()).Role
		if role != "" && !org.Statistics(role) && t.ActorID != identity.ActorID(r.Context()) {
			continue
		}
		data = append(data, s.generationView(r, t))
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"object": "list", "data": data, "has_more": more, "next_offset": offset + len(data)})
}

func (s *Server) createOpenAIGeneration(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var fields map[string]json.RawMessage
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		var ok bool
		fields, ok = s.multipartGeneration(w, r)
		if !ok {
			return
		}
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		d := json.NewDecoder(r.Body)
		if err := d.Decode(&fields); err != nil || fields == nil || d.Decode(&struct{}{}) != io.EOF {
			invalidParameter(w, "body", "send one JSON object, up to 64 KiB")
			return
		}
	}
	input, param, err := s.openAIInput(r, fields)
	if err != nil {
		invalidParameter(w, param, err.Error())
		return
	}
	if r.URL.Path == "/v1/generations/estimate" {
		s.estimateGeneration(w, r, input)
		return
	}
	s.submitGeneration(w, r, input)
}

func (s *Server) openAIInput(r *http.Request, fields map[string]json.RawMessage) (core.CreateMediaInput, string, error) {
	var input core.CreateMediaInput
	fail := func(p, m string) (core.CreateMediaInput, string, error) { return input, p, errors.New(m) }
	// Decode provider extensions separately from the public standard fields.
	// The internal task representation is not an alternative request format.
	base := map[string]json.RawMessage{}
	for _, k := range []string{"model", "prompt", "options", "webhook_url", "max_expected_cost_usd"} {
		if raw, ok := fields[k]; ok {
			base[k] = raw
			delete(fields, k)
		}
	}
	b, _ := json.Marshal(base)
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&input); err != nil {
		return fail("body", "invalid generation fields: "+err.Error())
	}
	if raw, ok := fields["timeout_seconds"]; ok {
		delete(fields, "timeout_seconds")
		var seconds int64
		if json.Unmarshal(raw, &seconds) != nil || string(raw) == "null" {
			return fail("timeout_seconds", "timeout_seconds must be a positive integer")
		}
		if err := core.ValidateTimeoutSeconds(&seconds); err != nil {
			return fail("timeout_seconds", err.Error())
		}
		input.Options.TimeoutSeconds = &seconds
	}
	want := ""
	switch r.URL.Path {
	case "/v1/videos":
		want = "video"
	case "/v1/images/generations", "/v1/images/edits":
		want = "image"
	case "/v1/audio/speech":
		want = "speech"
	case "/v1/audio/generations":
		want = "music"
	}
	input.MediaType = want
	if strings.TrimSpace(input.Model) == "" {
		return fail("model", "model is required; choose an ID from GET /v1/models")
	}
	var matches []struct {
		provider string
		model    core.MediaModel
	}
	for _, p := range s.visibleCatalog(r) {
		for _, m := range p.Models {
			if (input.Model == m.PublicID || m.PublicID == "" && input.Model == p.ID+"/"+m.ID) && (input.MediaType == "" || m.MediaType == input.MediaType || m.MediaType == "") {
				matches = append(matches, struct {
					provider string
					model    core.MediaModel
				}{p.ID, m})
			}
		}
	}
	if len(matches) == 0 {
		return fail("model", "model is not available for this media type and API key")
	}
	if len(matches) > 1 {
		return fail("model", "model ID is duplicated in the catalog")
	}
	input.PublicModel = input.Model
	input.Provider = matches[0].provider
	input.Model = matches[0].model.ID
	if input.MediaType == "" {
		input.MediaType = matches[0].model.MediaType
	}
	if input.MediaType == "speech" {
		if _, ok := base["prompt"]; ok {
			return fail("prompt", "use input for speech text")
		}
	}
	var options map[string]json.RawMessage
	if raw, ok := base["options"]; ok {
		if err := json.Unmarshal(raw, &options); err != nil || options == nil {
			return fail("options", "options must be an object")
		}
	}
	allowedOptions := map[string]bool{}
	for _, name := range []string{"parameters", "ratio", "generate_audio", "watermark", "camera_fixed", "draft", "seed", "return_last_frame", "negative_prompt", "reference_mode", "reference_voices", "video_urls", "audio_urls", "language", "lyrics", "gender"} {
		allowedOptions[name] = true
	}
	if input.MediaType == "music" || input.MediaType == "audio" || input.MediaType == "video" {
		allowedOptions["format"] = true
	}
	if input.MediaType == "music" || input.MediaType == "audio" {
		allowedOptions["duration"] = true
	}
	for name := range options {
		if !allowedOptions[name] {
			return fail("options."+name, "unsupported option: "+name+"; use the documented generation fields")
		}
	}
	assign := func(key string, target *string) error {
		raw, ok := fields[key]
		if !ok {
			return nil
		}
		delete(fields, key)
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || string(raw) == "null" {
			return fmt.Errorf("%s must be a string", key)
		}
		*target = value
		return nil
	}
	standardFields := []struct {
		key    string
		target *string
		media  string
	}{
		{"input", &input.Prompt, "speech"}, {"voice", &input.Options.Voice, "speech"}, {"instructions", &input.Options.Instructions, "speech"}, {"response_format", &input.Options.Format, "speech"},
		{"output_format", &input.Options.Format, "image"}, {"quality", &input.Options.Quality, "image"}, {"background", &input.Options.Background, "image"},
	}
	for _, a := range standardFields {
		if input.MediaType == a.media {
			if err := assign(a.key, a.target); err != nil {
				return fail(a.key, err.Error())
			}
		}
	}
	if raw, ok := fields["size"]; ok && (input.MediaType == "image" || input.MediaType == "video") {
		delete(fields, "size")
		var size string
		if json.Unmarshal(raw, &size) != nil || size == "" {
			return fail("size", "size must be a nonempty string")
		}
		resolution, ratio, err := modelSize(matches[0].model, size, input.MediaType)
		if err != nil {
			return fail("size", err.Error())
		}
		if ratio != "" && input.Options.Ratio != "" && input.Options.Ratio != ratio {
			return fail("size", "size conflicts with options.ratio")
		}
		input.Options.Resolution = resolution
		if ratio != "" {
			input.Options.Ratio = ratio
		}
	}
	if raw, ok := fields["seconds"]; ok && input.MediaType == "video" {
		delete(fields, "seconds")
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return fail("seconds", "seconds must be a string containing a positive integer")
		}
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 1 {
			return fail("seconds", "seconds must be a positive integer string")
		}
		input.Options.Duration = seconds
	}
	if raw, ok := fields["stream"]; ok {
		delete(fields, "stream")
		var stream bool
		if json.Unmarshal(raw, &stream) != nil || stream || string(raw) == "null" {
			return fail("stream", "streaming is not supported; poll the asynchronous generation instead")
		}
	}
	if input.MediaType == "image" {
		if raw, ok := fields["n"]; ok {
			delete(fields, "n")
			var n int
			if json.Unmarshal(raw, &n) != nil || n != 1 {
				return fail("n", "one output per generation is supported; submit separate asynchronous jobs")
			}
		}
		if raw, ok := fields["response_format"]; ok {
			delete(fields, "response_format")
			var format string
			if json.Unmarshal(raw, &format) != nil || format != "url" {
				return fail("response_format", "use url; results are downloaded from content_url after completion")
			}
		}
	}
	key := "image"
	if input.MediaType == "video" {
		key = "input_reference"
	}
	if raw, ok := fields[key]; ok && (input.MediaType == "video" || input.MediaType == "image") {
		delete(fields, key)
		urls, err := s.inputReferenceURLs(r, raw)
		if err != nil {
			return fail(key, err.Error())
		}
		input.Options.ImageURLs = urls
	}
	if r.URL.Path == "/v1/images/edits" && len(input.Options.ImageURLs) == 0 {
		return fail("image", "image is required for an edit")
	}
	for key := range fields {
		return fail(key, "unsupported parameter: "+key+"; provider-specific controls belong in options.parameters and must be advertised by the model")
	}
	return input, "", nil
}

func modelSize(model core.MediaModel, size, mediaType string) (string, string, error) {
	for _, r := range model.Resolutions {
		if strings.ReplaceAll(r, "*", "x") == size {
			return r, "", nil
		}
	}
	if mediaType == "image" {
		return size, "", nil
	} // Provider validation handles variable image dimensions.
	parts := strings.Split(size, "x")
	if len(parts) == 2 {
		w, e1 := strconv.Atoi(parts[0])
		h, e2 := strconv.Atoi(parts[1])
		if e1 == nil && e2 == nil && w > 0 && h > 0 {
			a, b := w, h
			for b != 0 {
				a, b = b, a%b
			}
			ratio := fmt.Sprintf("%d:%d", w/a, h/a)
			resolution := strconv.Itoa(min(w, h)) + "p"
			if slices.Contains(model.Resolutions, resolution) && slices.Contains(model.Ratios, ratio) {
				return resolution, ratio, nil
			}
		}
	}
	return "", "", errors.New("size does not match the model's resolutions and aspect ratios; choose size and options.ratio from the model catalog")
}
