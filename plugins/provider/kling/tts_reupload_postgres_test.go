package kling_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/Sociaro-Research/open-media-gateway/internal/queue"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/internal/worker"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
	"github.com/google/uuid"
)

type ttsReuploadTransport func(*http.Request) (*http.Response, error)

func (f ttsReuploadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type ttsReuploadHealth struct{}

func (ttsReuploadHealth) Check(context.Context) error { return nil }

type ttsReuploadProvider struct{ *kling.Adapter }

func (p ttsReuploadProvider) scope(ctx context.Context) context.Context {
	return core.WithProviderConnectionScope(ctx, core.ProviderConnectionScope{ID: "kling", OwnerID: identity.ProviderOwnerID(ctx), Revision: "tts-reupload-private-revision"})
}
func (p ttsReuploadProvider) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	return p.Adapter.ValidateRequest(p.scope(ctx), r)
}
func (p ttsReuploadProvider) Quote(ctx context.Context, m core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	cost, _, err := p.Adapter.Quote(p.scope(ctx), m, r)
	return cost, "tts-reupload-private-revision", err
}
func (p ttsReuploadProvider) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	return p.Adapter.Generate(p.scope(ctx), r)
}
func (p ttsReuploadProvider) GetTask(ctx context.Context, id string) (core.ProviderResult, error) {
	return p.Adapter.GetTask(p.scope(ctx), id)
}

func ttsReuploadServices(t *testing.T) (context.Context, *sql.DB, *queue.RedisQueue) {
	t.Helper()
	dsn, redis := os.Getenv("GATEWAY_TEST_DATABASE_URL"), os.Getenv("GATEWAY_TEST_REDIS_URL")
	if dsn == "" || redis == "" {
		t.Skip("requires disposable PostgreSQL and Redis")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	base, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Close() })
	schema := "tts_reupload_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Exec("DROP SCHEMA " + schema + " CASCADE") })
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := postgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = postgres.Migrate(db); err != nil {
		t.Fatal(err)
	}
	q, err := queue.Open(ctx, redis, "tts-reupload-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	return ctx, db, q
}
func ttsReuploadJSON(body any) *http.Response {
	raw, _ := json.Marshal(body)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: int64(len(raw)), Body: io.NopCloser(bytes.NewReader(raw))}
}
func ttsReuploadRequest(h http.Handler, method, path string, body any, auth bool) *httptest.ResponseRecorder {
	var raw bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&raw).Encode(body)
	}
	r := httptest.NewRequest(method, "https://gateway.fixture"+path, &raw)
	r.Header.Set("Content-Type", "application/json")
	if auth {
		r.Header.Set("Authorization", "Bearer tts-reupload-gateway-fixture")
	}
	if method == "POST" {
		r.Header.Set("Idempotency-Key", uuid.NewString())
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func ttsReuploadTaskID(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != 202 {
		t.Fatalf("admission %d: %s", w.Code, w.Body.String())
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.ID == "" {
		t.Fatal("missing task", err)
	}
	return result.ID
}
func ttsReuploadRun(t *testing.T, ctx context.Context, repo *postgres.TaskRepository, q *queue.RedisQueue, p ttsReuploadProvider, store *storage.Local, id string) core.Task {
	t.Helper()
	run, cancel := context.WithCancel(ctx)
	w := worker.New(repo, q, p, store, time.Millisecond, 5*time.Second, 2, time.Millisecond)
	w.OperationConcurrency = 1
	w.ArchiveConcurrency = 1
	done := make(chan error, 1)
	go func() { done <- w.Run(run) }()
	defer func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}()
	for {
		task, err := repo.Get(identity.WithOwnerID(ctx, identity.SystemOwnerID), id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == core.TaskSucceeded {
			return task
		}
		if task.Status == core.TaskFailed {
			t.Fatalf("worker failed: %v", task.Error)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}
func ttsReuploadWAV(seconds float64) []byte {
	data := make([]byte, 44+int(16000*seconds))
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 8000)
	binary.LittleEndian.PutUint32(data[28:], 16000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(len(data)-44))
	return data
}
func ttsReuploadMultipart(t *testing.T, h http.Handler, name string, data []byte, target *referencevalidation.Target) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	m := multipart.NewWriter(&body)
	part, err := m.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if target != nil {
		raw, _ := json.Marshal(target)
		if err = m.WriteField("validation", string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "https://gateway.fixture/v1/references", &body)
	r.Header.Set("Content-Type", m.FormDataContentType())
	r.Header.Set("Authorization", "Bearer tts-reupload-gateway-fixture")
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	return out
}
func ttsReuploadPrivate(t *testing.T, body string) {
	t.Helper()
	for _, private := range []string{"tts-native-private", "900719925474099312345", "second_audio_private", "face_session_private", "native_face_private", "tts-reupload-provider-fixture", "tts-reupload-private-revision", "https://cdn.fixture/"} {
		if strings.Contains(body, private) {
			t.Fatalf("private source exposed: %s", private)
		}
	}
}

// This is the existing explicit export/reupload workflow: no provider-resource
// creation, native-audio-ID consumer, new UI or production implementation is used.
func TestKlingTTSPostgresArchiveDownloadReuploadSoundFile(t *testing.T) {
	ctx, db, q := ttsReuploadServices(t)
	owner := identity.WithOwnerID(ctx, identity.SystemOwnerID)
	wav := ttsReuploadWAV(6)
	mp3 := testmedia.MP3()
	video := testmedia.MP4(6000, 1000, 180)
	at := bytes.Index(video, []byte("tkhd")) + 4
	binary.BigEndian.PutUint32(video[at+76:], 1280<<16)
	binary.BigEndian.PutUint32(video[at+80:], 720<<16)
	avatarImage := image.NewRGBA(image.Rect(0, 0, 512, 512))
	avatarImage.Set(0, 0, color.RGBA{R: 30, G: 60, B: 90, A: 255})
	var imageBytes bytes.Buffer
	if e := png.Encode(&imageBytes, avatarImage); e != nil {
		t.Fatal(e)
	}
	image := imageBytes.Bytes()
	var calls, downloads, avatarCalls, lipCalls atomic.Int32
	var expectedAudio, expectedImage string
	var host http.Handler
	checkSoundFile := func() error {
		if host == nil {
			return errors.New("host is unavailable")
		}
		delivered := ttsReuploadRequest(host, "GET", strings.TrimPrefix(expectedAudio, "https://gateway.fixture"), nil, false)
		if delivered.Code != 200 || !bytes.Equal(delivered.Body.Bytes(), wav) {
			return errors.New("sound_file capability did not serve exact owned reupload bytes")
		}
		return nil
	}
	native := &http.Client{Transport: ttsReuploadTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api-singapore.klingai.com" || r.Header.Get("Authorization") != "Bearer tts-reupload-provider-fixture" {
			return nil, errors.New("unexpected provider transport")
		}
		calls.Add(1)
		if r.Method == "GET" {
			var id, source string
			if strings.Contains(r.URL.Path, "/avatar/image2video/") {
				id, source = "avatar_fixture", "avatar.mp4"
			} else if strings.Contains(r.URL.Path, "/advanced-lip-sync/") {
				id, source = "lip_fixture", "lip.mp4"
			} else {
				return nil, errors.New("unexpected poll")
			}
			return ttsReuploadJSON(map[string]any{"code": 0, "data": map[string]any{"task_id": id, "task_status": "succeed", "task_result": map[string]any{"videos": []any{map[string]any{"id": "video_" + id, "url": "https://cdn.fixture/" + source, "duration": "6"}}}}}), nil
		}
		if r.Method != "POST" {
			return nil, errors.New("unexpected method")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		switch r.URL.Path {
		case "/v1/audio/tts":
			if body["text"] != "Synthetic fixture speech" || body["voice_id"] != "oversea_male1" || body["voice_language"] != "en" {
				return nil, errors.New("TTS intent changed")
			}
			return ttsReuploadJSON(map[string]any{"code": 0, "data": map[string]any{"task_id": "tts-native-private", "task_status": "succeed", "task_result": map[string]any{"audios": []any{map[string]any{"id": "900719925474099312345", "url": "https://cdn.fixture/tts.wav", "duration": "6"}, map[string]any{"id": "second_audio_private", "url": "https://cdn.fixture/tts.mp3", "duration": "2"}}}}}), nil
		case "/v1/videos/identify-face":
			if !strings.HasPrefix(fmt.Sprint(body["video_url"]), "https://gateway.fixture/references/") {
				return nil, errors.New("face source not owned reference")
			}
			return ttsReuploadJSON(map[string]any{"code": 0, "data": map[string]any{"session_id": "face_session_private", "face_data": []any{map[string]any{"face_id": "native_face_private", "start_time": 0, "end_time": 6000}}}}), nil
		case "/v1/videos/avatar/image2video":
			avatarCalls.Add(1)
			if err := checkSoundFile(); err != nil {
				return nil, err
			}
			if body["sound_file"] != expectedAudio || body["image"] != expectedImage || body["audio_id"] != nil || body["mode"] != "std" {
				return nil, fmt.Errorf("Avatar did not use reuploaded sound_file: %#v", body)
			}
			return ttsReuploadJSON(map[string]any{"code": 0, "data": map[string]any{"task_id": "avatar_fixture", "task_status": "submitted"}}), nil
		case "/v1/videos/advanced-lip-sync":
			lipCalls.Add(1)
			if err := checkSoundFile(); err != nil {
				return nil, err
			}
			faces, _ := body["face_choose"].([]any)
			if len(faces) != 1 {
				return nil, fmt.Errorf("lip faces: %#v", body)
			}
			face, _ := faces[0].(map[string]any)
			if body["session_id"] != "face_session_private" || face["face_id"] != "native_face_private" || face["sound_file"] != expectedAudio || face["audio_id"] != nil || face["sound_start_time"] != float64(0) || face["sound_end_time"] != float64(4000) || face["sound_insert_time"] != float64(0) || face["sound_volume"] != float64(1) || face["original_audio_volume"] != float64(1) {
				return nil, fmt.Errorf("lip did not use reuploaded sound_file: %#v", body)
			}
			return ttsReuploadJSON(map[string]any{"code": 0, "data": map[string]any{"task_id": "lip_fixture", "task_status": "submitted"}}), nil
		default:
			return nil, fmt.Errorf("unexpected route %s", r.URL.Path)
		}
	})}
	cdn := &http.Client{Transport: ttsReuploadTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "cdn.fixture" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			return nil, errors.New("unexpected CDN transport")
		}
		downloads.Add(1)
		data, kind := video, "video/mp4"
		switch r.URL.Path {
		case "/tts.wav":
			data, kind = wav, "audio/wav"
		case "/tts.mp3":
			data, kind = mp3, "audio/mpeg"
		case "/avatar.mp4", "/lip.mp4":
		default:
			return nil, errors.New("unexpected CDN path")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}
	raw, err := kling.Definition().Factory(providerkit.Credentials{APIKey: "tts-reupload-provider-fixture", Extra: map[string]string{"avatar_std_price": "0.1", "lip_sync_price": "0.1"}}, nil, native)
	if err != nil {
		t.Fatal(err)
	}
	provider := ttsReuploadProvider{raw.(*kling.Adapter)}
	repo := postgres.NewTaskRepository(db)
	resources := postgres.NewProviderResourceRepository(db)
	store, err := storage.NewLocal(t.TempDir(), cdn, 20<<20)
	if err != nil {
		t.Fatal(err)
	}
	library := references.New(postgres.NewReferenceRepository(db), store)
	service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"kling": provider}, map[string]string{"kling": kling.TextToSpeech}, "kling")
	service.EnableAccounting()
	scope := func(c context.Context, _ string) (core.ProviderConnectionScope, error) {
		return core.ProviderConnectionScope{ID: "kling", OwnerID: identity.ProviderOwnerID(c), Revision: "tts-reupload-private-revision"}, nil
	}
	h := api.NewGateway(api.Dependencies{Service: service, Health: ttsReuploadHealth{}, APIKey: "tts-reupload-gateway-fixture", Storage: store, SigningSecret: "tts-reupload-signing-fixture", MediaURLTTL: time.Hour, References: library, AssetPublicURL: "https://gateway.fixture", ResourceRepository: resources, ResourceScopes: scope})
	host = h
	speech := ttsReuploadRequest(h, "POST", "/v1/audio/speech", map[string]any{"model": "kling/" + kling.TextToSpeech, "input": "Synthetic fixture speech", "voice": "oversea_male1", "options": map[string]any{"language": "en"}}, true)
	ttsReuploadPrivate(t, speech.Body.String())
	speechID := ttsReuploadTaskID(t, speech)
	archived := ttsReuploadRun(t, ctx, repo, q, provider, store, speechID)
	if archived.ProviderTaskID != nil && *archived.ProviderTaskID != "" || len(archived.StoredArtifacts) != 2 || archived.StoredArtifacts[0].ProviderID != "900719925474099312345" {
		t.Fatal("synchronous TTS artifacts lost")
	}
	status := ttsReuploadRequest(h, "GET", "/v1/generations/"+speechID, nil, true)
	if status.Code != 200 {
		t.Fatal("archived TTS status unavailable", status.Code)
	}
	t.Run("private-output-identities", func(t *testing.T) { ttsReuploadPrivate(t, status.Body.String()) })
	var public struct {
		Artifacts []struct {
			Format     string `json:"format"`
			ContentURL string `json:"content_url"`
		} `json:"artifacts"`
	}
	if e := json.Unmarshal(status.Body.Bytes(), &public); e != nil || len(public.Artifacts) != 2 {
		t.Fatal("public TTS outputs missing", e)
	}
	content := ""
	for _, artifact := range public.Artifacts {
		if artifact.Format == "wav" {
			content = artifact.ContentURL + "?download=1"
		}
	}
	if content == "" {
		t.Fatal("public archived WAV URL missing")
	}
	beforeDownload := downloads.Load()
	downloaded := ttsReuploadRequest(h, "GET", content, nil, true)
	if downloaded.Code != 200 || !bytes.Equal(downloaded.Body.Bytes(), wav) || downloads.Load() != beforeDownload {
		t.Fatalf("archived audio download %d", downloaded.Code)
	}
	if unauth := ttsReuploadRequest(h, "GET", content, nil, false); unauth.Code != 401 {
		t.Fatal("unauthenticated archive read", unauth.Code)
	}
	target := &referencevalidation.Target{Provider: "kling", Model: kling.AvatarImageToVideo, Options: core.MediaOptions{ReferenceMode: "image_audio"}}
	uploaded := ttsReuploadMultipart(t, h, "downloaded-tts.wav", downloaded.Body.Bytes(), target)
	if uploaded.Code != 201 {
		t.Fatalf("reupload %d %s", uploaded.Code, uploaded.Body.String())
	}
	var uploadedRef struct {
		ID     string `json:"id"`
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	}
	if err = json.Unmarshal(uploaded.Body.Bytes(), &uploadedRef); err != nil {
		t.Fatal(err)
	}
	expectedAudio = "https://gateway.fixture" + uploadedRef.URL
	sum := sha256.Sum256(wav)
	if uploadedRef.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("reuploaded bytes changed")
	}
	imageRef, err := library.Upload(owner, "avatar.png", bytes.NewReader(image))
	if err != nil {
		t.Fatal(err)
	}
	expectedImage, _ = references.CanonicalReferenceURL("https://gateway.fixture", []byte("tts-reupload-signing-fixture"), identity.SystemOwnerID, imageRef.ID)
	videoRef, err := library.Upload(owner, "faces.mp4", bytes.NewReader(video))
	if err != nil {
		t.Fatal(err)
	}
	faceReply := ttsReuploadRequest(h, "POST", "/v1/media-inputs/faces", map[string]any{"provider": "kling", "workflow_model": kling.LipSync, "reference_id": videoRef.ID}, true)
	ttsReuploadPrivate(t, faceReply.Body.String())
	faceTask := ttsReuploadRun(t, ctx, repo, q, provider, store, ttsReuploadTaskID(t, faceReply))
	if len(faceTask.StoredResourceReferences) != 1 {
		t.Fatal("owned face session missing")
	}
	faceBinding := faceTask.StoredResourceReferences[0].BindingID
	avatarBody := map[string]any{"model": "kling/" + kling.AvatarImageToVideo, "input_reference": []string{expectedImage}, "options": map[string]any{"reference_mode": "image_audio", "audio_urls": []string{expectedAudio}, "parameters": map[string]any{"mode": "std"}}}
	lipBody := map[string]any{"model": "kling/" + kling.LipSync, "options": map[string]any{"reference_mode": "audio", "audio_urls": []string{expectedAudio}, "resource_references": []core.ResourceReference{{BindingID: faceBinding, ReferenceID: "face"}}, "parameters": map[string]any{"face_index": 1, "sound_start_time": 0, "sound_end_time": 4000, "sound_insert_time": 0}}}
	for _, body := range []map[string]any{avatarBody, lipBody} {
		t.Run(body["model"].(string), func(t *testing.T) {
			reply := ttsReuploadRequest(h, "POST", "/v1/videos", body, true)
			ttsReuploadPrivate(t, reply.Body.String())
			id := ttsReuploadTaskID(t, reply)
			task, err := repo.Get(owner, id)
			if err != nil {
				t.Fatal(err)
			}
			evidence := task.Options.ReferenceMediaEvidence[expectedAudio]
			if evidence.ReferenceID != uploadedRef.ID || evidence.Duration != 6 || evidence.SHA256 != uploadedRef.SHA256 || evidence.SizeBytes != int64(len(wav)) {
				t.Fatal("reupload owner evidence not inspected/pinned", evidence)
			}
			complete := ttsReuploadRun(t, ctx, repo, q, provider, store, id)
			if len(complete.StoredArtifacts) != 1 {
				t.Fatal("consumer output not archived")
			}
		})
	}
	if avatarCalls.Load() != 1 || lipCalls.Load() != 1 {
		t.Error("sound_file consumers not dispatched exactly once")
	}
	// A valid foreign capability is not authority to reuse another owner's input.
	foreign := identity.WithOwnerID(ctx, uuid.NewString())
	foreignRef, err := library.Upload(foreign, "foreign.wav", bytes.NewReader(wav))
	if err != nil {
		t.Fatal(err)
	}
	foreignURL, _ := references.CanonicalReferenceURL("https://gateway.fixture", []byte("tts-reupload-signing-fixture"), identity.OwnerID(foreign), foreignRef.ID)
	foreignBody := map[string]any{"model": "kling/" + kling.AvatarImageToVideo, "input_reference": []string{expectedImage}, "options": map[string]any{"reference_mode": "image_audio", "audio_urls": []string{foreignURL}}}
	before := calls.Load()
	if denied := ttsReuploadRequest(h, "POST", "/v1/videos", foreignBody, true); denied.Code != 404 {
		t.Fatal("foreign source admitted")
	}
	if calls.Load() != before {
		t.Fatal("foreign source contacted provider")
	}
	foreignTask := core.Task{ID: uuid.NewString(), OwnerID: identity.OwnerID(foreign), ProviderOwnerID: identity.OwnerID(foreign), StorageOwnerID: identity.OwnerID(foreign), Provider: "kling", Model: kling.TextToSpeech, MediaType: "speech", Status: core.TaskQueued, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if _, created, err := repo.CreateOrGet(foreign, foreignTask, "foreign-archive", "foreign-archive"); err != nil || !created {
		t.Fatal(err)
	}
	if err = repo.MarkRunning(foreign, foreignTask.ID); err != nil {
		t.Fatal(err)
	}
	first := archived.StoredArtifacts[0]
	if err = repo.MarkSucceeded(foreign, foreignTask.ID, core.StoredMedia{StorageKey: first.StorageKey, ContentType: first.ContentType, SizeBytes: first.SizeBytes, SHA256: first.SHA256, Artifacts: []core.StoredArtifact{first}}, ""); err != nil {
		t.Fatal(err)
	}
	if denied := ttsReuploadRequest(h, "GET", "/v1/generations/"+foreignTask.ID+"/artifacts/"+first.ID+"/content", nil, true); denied.Code != 404 {
		t.Fatal("foreign archived audio readable", denied.Code)
	}
	// An actual foreign face binding cannot be authorized by a forged private
	// snapshot supplied in upload validation metadata.
	foreignVideo, err := library.Upload(foreign, "foreign-faces.mp4", bytes.NewReader(video))
	if err != nil {
		t.Fatal(err)
	}
	ownedBinding, err := resources.GetBinding(owner, faceBinding)
	if err != nil {
		t.Fatal(err)
	}
	duration := 6.0
	foreignJob, created, err := resources.ReserveCreate(foreign, providerresources.Job{ProviderID: "kling", ConnectionID: "kling", Revision: "tts-reupload-private-revision", Intent: providerresources.Intent{Kind: "face_session", Name: "Foreign face fixture", Sources: []core.ResourceSourceEvidence{{ReferenceID: foreignVideo.ID, MediaType: "video", SHA256: ownedBinding.Session.SourceSHA256, Duration: &duration}}}}, "foreign-face-fixture")
	if err != nil || !created {
		t.Fatal(err)
	}
	if _, started, e := resources.StartJob(foreign, foreignJob.ID); e != nil || !started {
		t.Fatal(e)
	}
	foreignBinding, err := resources.CompleteResource(foreign, foreignJob.ID, core.ProviderResource{Kind: "face_session", RemoteID: "foreign_face_session_private", Status: core.ProviderSucceeded, Session: ownedBinding.Session})
	if err != nil {
		t.Fatal(err)
	}
	privateSnapshot := core.ResourceSnapshot{BindingID: foreignBinding.ID, ReferenceID: "face", Kind: "face_session", RemoteID: ownedBinding.RemoteID, ConnectionIdentity: ownedBinding.Revision, Session: ownedBinding.Session, Sources: ownedBinding.Sources}
	forged := &referencevalidation.Target{Provider: "kling", Model: kling.LipSync, Options: core.MediaOptions{ReferenceMode: "audio", ResourceReferences: []core.ResourceReference{{BindingID: foreignBinding.ID, ReferenceID: "face"}}, ResourceSnapshots: []core.ResourceSnapshot{privateSnapshot}, Parameters: map[string]any{"face_index": 1, "sound_start_time": 0, "sound_end_time": 4000, "sound_insert_time": 0}}}
	if denied := ttsReuploadMultipart(t, h, "forged-session.wav", wav, forged); denied.Code != 400 {
		t.Fatal("foreign face binding authorized by private snapshot", denied.Code)
	}
	foreignLip := map[string]any{"model": "kling/" + kling.LipSync, "options": map[string]any{"reference_mode": "audio", "audio_urls": []string{expectedAudio}, "resource_references": []core.ResourceReference{{BindingID: foreignBinding.ID, ReferenceID: "face"}}, "parameters": map[string]any{"face_index": 1, "sound_start_time": 0, "sound_end_time": 4000, "sound_insert_time": 0}}}
	if denied := ttsReuploadRequest(h, "POST", "/v1/videos", foreignLip, true); denied.Code < 400 {
		t.Fatal("foreign face session admitted")
	}
	for _, privateField := range []string{"_reference_resources", "session"} {
		options := lipBody["options"].(map[string]any)
		options[privateField] = []core.ResourceSnapshot{privateSnapshot}
		denied := ttsReuploadRequest(h, "POST", "/v1/videos", lipBody, true)
		delete(options, privateField)
		if denied.Code != 400 {
			t.Fatal("client private session metadata accepted", privateField, denied.Code)
		}
	}
	// Existing sound_file limits apply to exported bytes; no native-ID shortcut.
	exact := ttsReuploadWAV(6)
	padding := make([]byte, (5<<20)-len(exact))
	copy(padding, "JUNK")
	binary.LittleEndian.PutUint32(padding[4:], uint32(len(padding)-8))
	exact = append(exact, padding...)
	binary.LittleEndian.PutUint32(exact[4:], uint32(len(exact)-8))
	for _, limitCase := range []struct {
		name        string
		data        []byte
		model, mode string
	}{{"exact-five-MiB", exact, kling.AvatarImageToVideo, "image_audio"}, {"short", ttsReuploadWAV(1.9), kling.AvatarImageToVideo, "image_audio"}, {"avatar-overlong", ttsReuploadWAV(301), kling.AvatarImageToVideo, "image_audio"}, {"lip-overlong", ttsReuploadWAV(61), kling.LipSync, "audio"}} {
		t.Run(limitCase.name, func(t *testing.T) {
			out := ttsReuploadMultipart(t, h, "bounded.wav", limitCase.data, &referencevalidation.Target{Provider: "kling", Model: limitCase.model, Options: core.MediaOptions{ReferenceMode: limitCase.mode}})
			if out.Code != 400 {
				t.Fatalf("sound_file limit bypass: %d %s", out.Code, out.Body.String())
			}
		})
	}
	for _, body := range []map[string]any{avatarBody, lipBody} {
		options := body["options"].(map[string]any)
		parameters := options["parameters"].(map[string]any)
		parameters["audio_id"] = "client-asserted-native-id"
		if denied := ttsReuploadRequest(h, "POST", "/v1/videos", body, true); denied.Code != 400 {
			t.Fatal("client native audio ID accepted", denied.Code)
		}
		delete(parameters, "audio_id")
	}
	if calls.Load() != before {
		t.Fatal("rejected sound_file/native-ID inputs reached provider")
	}
	var localBindings int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM provider_resources WHERE kind<>'face_session'`).Scan(&localBindings); err != nil || localBindings != 0 {
		t.Fatal("export/reupload created a new provider resource", localBindings, err)
	}
}
