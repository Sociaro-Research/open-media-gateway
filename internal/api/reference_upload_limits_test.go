package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
)

func uploadLimitKlingModel(t *testing.T) core.MediaModel {
	t.Helper()
	adapter, err := kling.New("fixture", "https://api-singapore.klingai.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range adapter.ReviewedCatalogModels() {
		if model.ID == "kling-4.0-omni-video" {
			return model
		}
	}
	t.Fatal("native model missing")
	return core.MediaModel{}
}

func TestReferenceUploadHostTargetAdmission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target bool
		size   int64
		status int
	}{
		{"ordinary exact", false, references.MaxBytes, 201},
		{"ordinary over", false, references.MaxBytes + 1, 400},
		{"reviewed decimal exact", true, references.HardMaxBytes, 201},
		{"reviewed decimal over", true, references.HardMaxBytes + 1, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, svc, local := templateFixture(t)
			svc.catalog = []core.ProviderDescriptor{{ID: "actual-connection", Models: []core.MediaModel{uploadLimitKlingModel(t)}}}
			repo := &audioUploadRepository{referenceMemory: &referenceMemory{items: map[string]references.Image{}}, sessions: map[string]references.UploadSession{}}
			s.references = references.New(repo, &audioUploadStore{Local: local})
			h := s.handler()
			_, key := issueTestKey(t, h, "alice", `{"name":"target-limit"}`)
			body := map[string]any{"name": "synthetic.mp4", "content_type": "video/mp4", "size_bytes": tc.size, "sha256": strings.Repeat("a", 64)}
			if tc.target {
				body["validation"] = &referencevalidation.Target{Provider: "actual-connection", Model: "kling-4.0-omni-video", Options: core.MediaOptions{ReferenceMode: "references"}}
			}
			raw, _ := json.Marshal(body)
			out := sendTest(h, "POST", "/v1/references/uploads", string(raw), "", key)
			if out.Code != tc.status {
				t.Fatalf("status %d want %d: %s", out.Code, tc.status, out.Body.String())
			}
			if len(repo.sessions) > 0 && tc.status != 201 {
				t.Fatal("invalid upload persisted session")
			}
			if svc.calls != 0 {
				t.Fatal("upload admission generated provider task")
			}
		})
	}
}

func TestReferenceUploadAuthorizedCatalogAndNativePolicy(t *testing.T) {
	s, _, svc, _ := templateFixture(t)
	base := uploadLimitKlingModel(t)
	for _, tc := range []struct {
		name       string
		change     func(*core.MediaModel)
		role, mode string
		want       int64
		bad        bool
	}{
		{name: "reviewed default workflow", want: references.HardMaxBytes},
		{name: "manual cannot extend", change: func(m *core.MediaModel) { m.Manual = true }, want: references.MaxBytes},
		{name: "ordinary unspecified", change: func(m *core.MediaModel) { m.ReferenceMediaLimits = nil }, want: references.MaxBytes},
		{name: "smaller exclusive", change: func(m *core.MediaModel) {
			m.ReferenceMediaLimits = m.ReferenceMediaLimits.Clone()
			m.ReferenceMediaLimits.Video.MaxBytes = 1000
			m.ReferenceMediaLimits.Video.MaxBytesInclusive = false
		}, want: 999},
		{name: "smaller inclusive", change: func(m *core.MediaModel) {
			m.ReferenceMediaLimits = m.ReferenceMediaLimits.Clone()
			m.ReferenceMediaLimits.Video.MaxBytes = 1000
		}, want: 1000},
		{name: "image workflow cannot extend", change: func(m *core.MediaModel) { m.ReferenceModes = []core.ReferenceMode{{ID: "images", MaxImages: 1}} }, mode: "images", want: references.MaxBytes},
		{name: "reference voice cannot borrow video cap", role: "reference_voice", want: references.MaxBytes},
		{name: "unknown role", role: "native_remote_id", bad: true},
		{name: "unknown workflow", mode: "invented", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := base
			if tc.change != nil {
				tc.change(&model)
			}
			svc.catalog = []core.ProviderDescriptor{{ID: "reviewed-connection", Models: []core.MediaModel{model}}}
			r := httptest.NewRequest("POST", "/v1/references/uploads", nil)
			limit, err := s.referenceUploadLimit(r, &referencevalidation.Target{Provider: "reviewed-connection", Model: model.ID, Role: tc.role, Options: core.MediaOptions{ReferenceMode: tc.mode}})
			if limit != tc.want || (err != nil) != tc.bad {
				t.Fatalf("limit=%d want=%d err=%v", limit, tc.want, err)
			}
		})
	}
	r := httptest.NewRequest("POST", "/v1/references/uploads", nil)
	svc.catalog = nil
	if _, err := s.referenceUploadLimit(r, &referencevalidation.Target{Provider: "reviewed-connection", Model: base.ID}); !errors.Is(err, referencevalidation.ErrInvalid) {
		t.Fatal("hidden catalog extended upload", err)
	}
	if limit, err := s.referenceUploadLimit(r, nil); err != nil || limit != references.MaxBytes {
		t.Fatal(limit, err)
	}
}

func TestReferenceDirectCompletionRechecksCurrentHostAndOwner(t *testing.T) {
	s, _, svc, local := templateFixture(t)
	model := uploadLimitKlingModel(t)
	svc.catalog = []core.ProviderDescriptor{{ID: "reviewed-connection", Models: []core.MediaModel{model}}}
	repo := &audioUploadRepository{referenceMemory: &referenceMemory{items: map[string]references.Image{}}, sessions: map[string]references.UploadSession{}}
	s.references = references.New(repo, &audioUploadStore{Local: local})
	h := s.handler()
	_, alice := issueTestKey(t, h, "alice", `{"name":"alice-upload"}`)
	_, bob := issueTestKey(t, h, "bob", `{"name":"bob-upload"}`)
	raw, _ := json.Marshal(references.UploadInput{Name: "synthetic.mp4", ContentType: "video/mp4", SizeBytes: references.HardMaxBytes, SHA256: strings.Repeat("a", 64), Validation: &referencevalidation.Target{Provider: "reviewed-connection", Model: model.ID, Options: core.MediaOptions{ReferenceMode: "references"}}})
	out := sendTest(h, "POST", "/v1/references/uploads", string(raw), "", alice)
	if out.Code != 201 {
		t.Fatal(out.Code, out.Body.String())
	}
	var plan references.UploadPlan
	if err := json.Unmarshal(out.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	path := "/v1/references/uploads/" + plan.ID + "/complete"
	if foreign := sendTest(h, "POST", path, "{}", "", bob); foreign.Code != 404 {
		t.Fatal("foreign completion", foreign.Code, foreign.Body.String())
	}
	svc.catalog[0].Models[0].Manual = true
	if changed := sendTest(h, "POST", path, "{}", "", alice); changed.Code != 400 {
		t.Fatal("changed native policy completed", changed.Code, changed.Body.String())
	}
	svc.catalog = nil
	if hidden := sendTest(h, "POST", path, "{}", "", alice); hidden.Code != 400 {
		t.Fatal("hidden model completed", hidden.Code, hidden.Body.String())
	}
}

func TestResourceSourceUsesSelectedVariantAndDefaultVideoCeiling(t *testing.T) {
	base := core.MediaModel{ResourceOperation: &core.ResourceOperationSchema{Kind: "voice", Subtypes: []string{"custom"}, Variants: []core.ResourceVariantSchema{{Subtype: "custom", SourceKinds: []string{"audio", "video"}}}}}
	file := referencevalidation.File{Type: "video", Name: "owned.mp4", Info: mediafile.Info{Size: references.MaxBytes + 1, ContentType: "video/mp4"}}
	if err := validateResourceSourceFile(base, "custom", file); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("resource source borrowed global read ceiling", err)
	}
	base.ReferenceMediaLimits = &core.ReferenceMediaLimits{Video: core.MediaFileLimits{MaxBytes: references.HardMaxBytes, MaxBytesInclusive: true}}
	if err := validateResourceSourceFile(base, "custom", file); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("resource source borrowed generation allowance", err)
	}
	base.ResourceOperation.Variants[0].Limits = &core.ReferenceMediaLimits{Video: core.MediaFileLimits{MaxBytes: 1000, MaxBytesInclusive: true}}
	file.Info.Size = 1001
	if err := validateResourceSourceFile(base, "custom", file); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("selected source limit ignored", err)
	}
	file.Info.Size = 1000
	if err := validateResourceSourceFile(base, "custom", file); err != nil {
		t.Fatal(err)
	}
	file.Type = "image"
	if err := validateResourceSourceFile(base, "custom", file); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("wrong source kind", err)
	}
}

func uploadLimitSparseVideo(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "synthetic-extended-*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	header := testmedia.MP4(5000, 1000, 150)
	index := bytes.Index(header, []byte("tkhd"))
	if index < 0 {
		t.Fatal("fixture track missing")
	}
	binary.BigEndian.PutUint32(header[index+4+76:], 1280<<16)
	binary.BigEndian.PutUint32(header[index+4+80:], 720<<16)
	if _, err = file.Write(header); err != nil {
		t.Fatal(err)
	}
	padding := make([]byte, 8)
	binary.BigEndian.PutUint32(padding, uint32(references.MaxBytes+1-int64(len(header))))
	copy(padding[4:], "free")
	if _, err = file.Write(padding); err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(references.MaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	return file
}

func uploadLimitStreamMultipart(t *testing.T, h http.Handler, path, key, actor string, file *os.File, target *referencevalidation.Target) *httptest.ResponseRecorder {
	t.Helper()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	multi := multipart.NewWriter(writer)
	contentType := multi.FormDataContentType()
	done := make(chan error, 1)
	go func() {
		part, err := multi.CreateFormFile("file", "synthetic-extended.mp4")
		if err == nil {
			_, err = io.Copy(part, file)
		}
		if err == nil && target != nil {
			raw, _ := json.Marshal(target)
			err = multi.WriteField("validation", string(raw))
		}
		if err == nil {
			err = multi.Close()
		}
		_ = writer.CloseWithError(err)
		done <- err
	}()
	r := httptest.NewRequest("POST", path, reader)
	r.Header.Set("Content-Type", contentType)
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if actor != "" {
		r.AddCookie(&http.Cookie{Name: "test_session", Value: actor})
	}
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	reader.Close()
	if err := <-done; err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	return out
}

func TestExtendedVideoHostMultipartTemplateAndResourceAdmission(t *testing.T) {
	if testing.Short() {
		t.Skip("runtime-generated 200 MiB boundary fixture")
	}
	s, templateRepo, svc, _ := templateFixture(t)
	native := uploadLimitKlingModel(t)
	manual := native
	manual.ID = "manual-copy"
	manual.Manual = true
	ordinary := native
	ordinary.ID = "ordinary"
	ordinary.ReferenceMediaLimits = nil
	svc.catalog = []core.ProviderDescriptor{{ID: "reviewed-connection", Models: []core.MediaModel{native, manual, ordinary}}}
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"large-input"}`)
	file := uploadLimitSparseVideo(t)
	target := &referencevalidation.Target{Provider: "reviewed-connection", Model: native.ID, Options: core.MediaOptions{ReferenceMode: "references"}}
	for _, model := range []string{"", manual.ID} {
		var selected *referencevalidation.Target
		if model != "" {
			v := *target
			v.Model = model
			selected = &v
		}
		out := uploadLimitStreamMultipart(t, h, "/v1/references", key, "", file, selected)
		if out.Code != 400 {
			t.Fatalf("ordinary/manual oversized multipart accepted: %d %s", out.Code, out.Body.String())
		}
	}
	out := uploadLimitStreamMultipart(t, h, "/v1/references", key, "", file, target)
	if out.Code != 201 {
		t.Fatalf("reviewed oversized multipart: %d %s", out.Code, out.Body.String())
	}
	var saved referenceResponse
	if err := json.Unmarshal(out.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	nilValidation, _ := json.Marshal(map[string]any{"validation": nil, "reference_ids": []string{saved.ID}})
	if checked := sendTest(h, "POST", "/v1/references/validate", string(nilValidation), "", key); checked.Code != 400 {
		t.Fatalf("niltarget validation borrowed extended ceiling: %d %s", checked.Code, checked.Body.String())
	}
	for _, model := range []string{native.ID, manual.ID, ordinary.ID} {
		selected := *target
		selected.Model = model
		raw, _ := json.Marshal(map[string]any{"validation": selected, "reference_ids": []string{saved.ID}})
		checked := sendTest(h, "POST", "/v1/references/validate", string(raw), "", key)
		expected := 400
		if model == native.ID {
			expected = 200
		}
		if checked.Code != expected {
			t.Fatalf("stored input %s: %d %s", model, checked.Code, checked.Body.String())
		}
	}
	owner := strings.Split(saved.URL, "/")[2]
	ctx := identity.WithOwnerID(context.Background(), owner)
	request := httptest.NewRequest("POST", "/v1/provider-resources", nil).WithContext(ctx)
	voice := core.MediaModel{ResourceOperation: &core.ResourceOperationSchema{Kind: "voice", Subtypes: []string{"custom"}, Variants: []core.ResourceVariantSchema{{Subtype: "custom", SourceKinds: []string{"audio", "video"}}}}}
	if _, err := s.resolveResourceCreation(request, &core.ResourceIntent{Kind: "voice", Subtype: "custom", Name: "voice", ReferenceIDs: []string{saved.ID}}, "reviewed-connection", voice); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("owned large file bypassed resource default", err)
	}
	shared := uploadLimitStreamMultipart(t, h, "/v1/system/template-references", "", "alice", file, target)
	if shared.Code != 201 {
		t.Fatalf("template library upload: %d %s", shared.Code, shared.Body.String())
	}
	var source referenceResponse
	if err := json.Unmarshal(shared.Body.Bytes(), &source); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	templateRepo.items[id] = templates.Record{ID: id}
	for _, model := range []string{"", manual.ID, native.ID} {
		var selected *referencevalidation.Target
		if model != "" {
			v := *target
			v.Model = model
			selected = &v
		}
		raw, _ := json.Marshal(map[string]any{"library_reference_id": source.ID, "validation": selected})
		promoted := sendTest(h, "POST", "/v1/system/templates/"+id+"/assets", string(raw), "alice", "")
		expected := 400
		if model == native.ID {
			expected = 201
		}
		if promoted.Code != expected {
			t.Fatalf("template promotion %s: %d %s", model, promoted.Code, promoted.Body.String())
		}
		if expected == 201 {
			var asset templates.Asset
			if err := json.Unmarshal(promoted.Body.Bytes(), &asset); err != nil {
				t.Fatal(err)
			}
			asset = templateRepo.assets[asset.ID]
			info, err := s.inspectTemplateMedia(templateAssetStorageContext(context.Background(), asset), asset.StorageKey, asset.ContentType, asset.SizeBytes, asset.SHA256)
			if err != nil || info.Size != references.MaxBytes+1 {
				t.Fatalf("template physical read: %+v %v", info, err)
			}
		}
	}
	if svc.calls != 0 {
		t.Fatal("upload/validation called generation")
	}
}

func TestReferenceUploadRejectsHiddenModelAndClientLimitControls(t *testing.T) {
	s, _, svc, local := templateFixture(t)
	model := uploadLimitKlingModel(t)
	svc.catalog = []core.ProviderDescriptor{{ID: "actual-connection", Models: []core.MediaModel{model}}}
	repo := &audioUploadRepository{referenceMemory: &referenceMemory{items: map[string]references.Image{}}, sessions: map[string]references.UploadSession{}}
	s.references = references.New(repo, &audioUploadStore{Local: local})
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"visibility-limit"}`)
	target := &referencevalidation.Target{Provider: "actual-connection", Model: model.ID, Options: core.MediaOptions{ReferenceMode: "references"}}
	body := map[string]any{"name": "synthetic.mp4", "content_type": "video/mp4", "size_bytes": references.HardMaxBytes, "sha256": strings.Repeat("a", 64), "validation": target}
	raw, _ := json.Marshal(body)
	policy := &modelAccessMemory{org: map[string]map[string]bool{}}
	if err := policy.Ensure(context.Background(), []modelaccess.Ref{{Provider: "actual-connection", Model: model.ID}}); err != nil {
		t.Fatal(err)
	}
	policy.models[0].EnabledByDefault = false
	s.modelAccess = policy
	if out := sendTest(s.handler(), "POST", "/v1/references/uploads", string(raw), "", key); out.Code != 400 {
		t.Fatal("hidden model admitted upload", out.Code, out.Body.String())
	}
	s.modelAccess = nil
	body["max_bytes"] = references.HardMaxBytes
	raw, _ = json.Marshal(body)
	if out := sendTest(s.handler(), "POST", "/v1/references/uploads", string(raw), "", key); out.Code != 400 {
		t.Fatal("client byte-limit control accepted", out.Code, out.Body.String())
	}
	if len(repo.sessions) != 0 || svc.calls != 0 {
		t.Fatal("invalid target caused mutation")
	}
}
