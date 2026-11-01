package api

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
	"github.com/google/uuid"
)

// Exercise the real upload handlers and library with local bytes instead of S3.
// Completion verifies the declared checksum/size before storing any reference.
type audioUploadRepository struct {
	*referenceMemory
	sessions map[string]references.UploadSession
}

func (m *audioUploadRepository) CreateUpload(ctx context.Context, s references.UploadSession) error {
	m.sessions[identity.OwnerID(ctx)+"/"+s.Item.ID] = s
	return nil
}
func (m *audioUploadRepository) UseUpload(ctx context.Context, id string, _ bool, fn func(context.Context, *references.UploadSession, references.Repository) error) error {
	key := identity.OwnerID(ctx) + "/" + id
	s, ok := m.sessions[key]
	if !ok {
		return storage.ErrObjectNotFound
	}
	if err := fn(ctx, &s, m); err != nil {
		return err
	}
	m.sessions[key] = s
	return nil
}
func (*audioUploadRepository) ExpiredUploads(context.Context) ([]references.ExpiredUpload, error) {
	return nil, nil
}

type audioUploadStore struct {
	*storage.Local
	data []byte
}

func (s *audioUploadStore) PlanUpload(_ context.Context, id, kind, digest string, size int64) (*storage.PendingUpload, error) {
	return &storage.PendingUpload{Key: id, ContentType: kind, SHA256: digest, Size: size}, nil
}
func (*audioUploadStore) StartUpload(_ context.Context, p *storage.PendingUpload) (storage.UploadTarget, error) {
	p.UploadID = "local-fixture"
	return storage.UploadTarget{URL: "https://upload.invalid/never-fetched"}, nil
}
func (s *audioUploadStore) CompleteUpload(ctx context.Context, p storage.PendingUpload) (core.StoredMedia, error) {
	sum := sha256.Sum256(s.data)
	if p.Size != int64(len(s.data)) || p.SHA256 != hex.EncodeToString(sum[:]) {
		return core.StoredMedia{}, storage.ErrInvalidConfig
	}
	return s.Local.StoreReference(ctx, p.Key, s.data, p.ContentType)
}
func (*audioUploadStore) AbortUpload(context.Context, storage.PendingUpload, bool) error { return nil }

func templateAudioWAV(seconds float64) []byte {
	b := make([]byte, 44+int(16000*seconds))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 8000)
	binary.LittleEndian.PutUint32(b[28:], 16000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(b)-44))
	return b
}
func templateAudioMP3() []byte {
	b := make([]byte, 417*80)
	for i := 0; i < 80; i++ {
		copy(b[417*i:], []byte{0xff, 0xfb, 0x90, 0})
	}
	return b
}

type audioEstimateService struct {
	*openAIServiceFixture
	estimates int
}

func (s *audioEstimateService) Estimate(_ context.Context, input core.CreateMediaInput) (core.CostEstimate, error) {
	s.estimates++
	s.input = input
	return core.CostEstimate{CanGenerate: true}, nil
}

func TestTemplateAudioUploadEstimate(t *testing.T) {
	for _, tc := range []struct {
		name, kind, legacy string
		data               []byte
		status             int
	}{
		{"wav", "audio/wav", "", templateAudioWAV(2), 200},
		{"wave", "audio/wave", "", templateAudioWAV(2), 200},
		{"x-wav", "audio/x-wav", "", templateAudioWAV(2), 200},
		{"mp3", "audio/mpeg", "", templateAudioMP3(), 200},
		{"legacy-wav", "audio/wave", "audio/wav", templateAudioWAV(2), 200},
		{"legacy-x-wav", "audio/wave", "audio/x-wav", templateAudioWAV(2), 200},
		{"malformed", "audio/wav", "", templateAudioWAV(2)[:20], 400},
		{"different-format", "audio/wav", "", templateAudioMP3(), 400},
		{"unsupported-flac", "audio/flac", "", []byte("fLaCunsupported"), 400},
		{"minimum-duration", "audio/wav", "", templateAudioWAV(1), 200},
		{"maximum-duration", "audio/x-wav", "", templateAudioWAV(15), 200},
		{"bad-hash", "audio/wav", "", templateAudioWAV(2), 404},
		{"bad-size", "audio/x-wav", "", templateAudioWAV(2), 404},
		{"too-short", "audio/wav", "", templateAudioWAV(.5), 400},
		{"too-long", "audio/wav", "", templateAudioWAV(16), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, svc, store := templateFixture(t)
			counter := &audioEstimateService{openAIServiceFixture: svc}
			s.service = counter
			var model core.MediaModel
			for _, m := range alibaba.Definition().Profiles() {
				if m.ID == "wan3.0-video" {
					model = m
				}
			}
			if model.ID == "" {
				t.Fatal("missing native Wan3 profile")
			}
			svc.catalog = []core.ProviderDescriptor{{ID: "alibaba", Models: []core.MediaModel{model}}}
			id := uuid.NewString()
			d := templates.Definition{Title: "Audio reference", MediaType: "video", Provider: "alibaba", Model: model.ID, Prompt: "Audio 1 accompanies a landscape", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Duration: 5}}
			var err error
			d.Slots, err = d.InferSlots(model)
			if err != nil {
				t.Fatal(err)
			}
			repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
			d.CoverID = templateCopy(t, s, id, referencePNG(t)).ID
			body, _ := json.Marshal(map[string]any{"version": 1, "action": "publish", "draft": d})
			if w := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			uploads := &audioUploadRepository{referenceMemory: &referenceMemory{items: map[string]references.Image{}}, sessions: map[string]references.UploadSession{}}
			s.references = references.New(uploads, &audioUploadStore{Local: store, data: tc.data})
			_, key := issueTestKey(t, s.handler(), "alice", `{"name":"Audio regression"}`)
			_, foreignKey := issueTestKey(t, s.handler(), "bob", `{"name":"Other owner"}`)
			sum := sha256.Sum256(tc.data)
			digest := hex.EncodeToString(sum[:])
			body, _ = json.Marshal(references.UploadInput{Name: "audio", ContentType: tc.kind, SizeBytes: int64(len(tc.data)), SHA256: digest})
			start := sendTest(s.handler(), "POST", "/v1/references/uploads", string(body), "", key)
			if start.Code != 201 {
				t.Fatal(start.Code, start.Body.String())
			}
			var plan references.UploadPlan
			if err = json.Unmarshal(start.Body.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			finishPath := "/v1/references/uploads/" + plan.ID + "/complete"
			if w := sendTest(s.handler(), "POST", finishPath, "{}", "", foreignKey); w.Code != 404 {
				t.Fatal("foreign upload completion", w.Code)
			}
			finish := sendTest(s.handler(), "POST", finishPath, "{}", "", key)
			// The common upload validator now rejects malformed/mismatched bytes
			// before a completed/selectable reference exists, rather than at estimate.
			if tc.name == "malformed" || tc.name == "different-format" || tc.name == "unsupported-flac" {
				if finish.Code != 400 || len(uploads.items) != 0 || counter.estimates != 0 {
					t.Fatal("invalid audio retained or estimated", finish.Code, finish.Body.String())
				}
				if retry := sendTest(s.handler(), "POST", finishPath, "{}", "", key); retry.Code != 400 {
					t.Fatal("rejected completion retry", retry.Code)
				}
				return
			}
			if finish.Code != 200 {
				t.Fatal(finish.Code, finish.Body.String())
			}
			if retry := sendTest(s.handler(), "POST", finishPath, "{}", "", key); retry.Code != 200 || retry.Body.String() != finish.Body.String() {
				t.Fatal("completion retry changed", retry.Code)
			}
			var ref referenceResponse
			if err = json.Unmarshal(finish.Body.Bytes(), &ref); err != nil {
				t.Fatal(err)
			}
			wantKind := tc.kind
			if strings.HasPrefix(tc.name, "legacy-") || tc.kind == "audio/wav" || tc.kind == "audio/x-wav" {
				wantKind = "audio/wave"
			}
			if ref.ContentType != wantKind {
				t.Fatalf("upload type %q, want %q", ref.ContentType, wantKind)
			}
			// Existing metadata must also work without a migration or reupload.
			if tc.legacy != "" {
				for k, v := range uploads.items {
					if v.ID == ref.ID {
						v.ContentType = tc.legacy
						uploads.items[k] = v
					}
				}
			}
			for k, v := range uploads.items {
				if v.ID != ref.ID {
					continue
				}
				switch tc.name {
				case "bad-hash":
					v.SHA256 = strings.Repeat("0", 64)
				case "bad-size":
					v.SizeBytes++
				}
				uploads.items[k] = v
			}
			foreignOrg := identity.WithOwnerID(context.Background(), uuid.NewString())
			if _, err := s.references.Get(foreignOrg, ref.ID); err != storage.ErrObjectNotFound {
				t.Fatal("foreign organization read", err)
			}
			if ref.SHA256 != digest || ref.SizeBytes != int64(len(tc.data)) {
				t.Fatal("upload identity changed", ref)
			}
			request, _ := json.Marshal(map[string]any{"revision": 2, "provider": "alibaba", "model": model.ID, "inputs": map[string]string{"audio:1": ref.ID}})
			estimatePath := "/v1/templates/" + id + "/estimate"
			if w := sendTest(s.handler(), "POST", estimatePath, string(request), "", foreignKey); w.Code != 400 || counter.estimates != 0 {
				t.Fatal("foreign reference admitted", w.Code)
			}
			w := sendTest(s.handler(), "POST", estimatePath, string(request), "", key)
			if w.Code != tc.status {
				t.Fatalf("estimate %s: %d %s", tc.kind, w.Code, w.Body.String())
			}
			if tc.status == 200 {
				if counter.estimates != 1 || !strings.Contains(w.Body.String(), `"can_generate":true`) {
					t.Fatal("missing estimate", w.Body.String())
				}
				input := counter.input
				if input.Provider != "alibaba" || input.Model != model.ID || input.Prompt != d.Prompt || len(input.Options.AudioURLs) != 1 || !strings.Contains(input.Options.AudioURLs[0], ref.ID) || len(input.TemplateSnapshot) == 0 {
					t.Fatal("incorrect request assembly", input)
				}
			} else if counter.estimates != 0 {
				t.Fatal("invalid media reached estimator")
			}
			// Hash deduplication is independent of a WAV label or filename.
			duplicateBody, _ := json.Marshal(references.UploadInput{Name: "renamed", ContentType: tc.kind, SizeBytes: int64(len(tc.data)), SHA256: digest})
			if tc.status == 200 {
				duplicate := sendTest(s.handler(), "POST", "/v1/references/uploads", string(duplicateBody), "", key)
				if duplicate.Code != 200 || !strings.Contains(duplicate.Body.String(), ref.ID) {
					t.Fatal("deduplication failed", duplicate.Code, duplicate.Body.String())
				}
			}
			if svc.calls != 0 {
				t.Fatal("generation invoked")
			}
		})
	}
}

// Keep the multipart upload path byte-driven even if a filename suggests PNG.
func TestTemplateAudioLocalUploadBytes(t *testing.T) {
	s, _, _, _ := templateFixture(t)
	r := referenceUploadRequest(t, templateAudioWAV(2))
	_, key := issueTestKey(t, s.handler(), "alice", `{"name":"Local audio"}`)
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var ref referenceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &ref); err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: "alice"}))
	item, err := s.references.Get(ctx, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.inspectTemplateMedia(ctx, item.StorageKey, item.ContentType, item.SizeBytes, item.SHA256)
	if err != nil || info.Duration != 2 {
		t.Fatal(info, err)
	}
}
