package minimax_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/queue"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/internal/worker"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/minimax"
	"github.com/google/uuid"
)

type resourceWorkerProvider struct {
	workerScope
	opener core.ResourceSourceOpener
}

func (p resourceWorkerProvider) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	return p.workerScope.Generate(core.WithResourceSourceOpener(ctx, p.opener), r)
}

type resourceDirectory struct{ p core.MediaProvider }

func (d resourceDirectory) Providers(context.Context) (map[string]core.MediaProvider, error) {
	return map[string]core.MediaProvider{"minimax": d.p}, nil
}
func (d resourceDirectory) Provider(_ context.Context, id string) (core.MediaProvider, error) {
	if id != "minimax" {
		return nil, core.ErrProviderUnavailable
	}
	return d.p, nil
}
func pgResourceScope(ctx context.Context, _ string) (core.ProviderConnectionScope, error) {
	return core.ProviderConnectionScope{ID: "minimax", OwnerID: identity.ProviderOwnerID(ctx), Revision: "integration-revision"}, nil
}

// Conforming MiniMax source: inspected MP4, 24fps, 124frames, 768x1024, audio track.
func inspectedBaseVideoFixture() []byte {
	data := testmedia.MP4(124, 24, 124)
	index := bytes.Index(data, []byte("tkhd"))
	binary.BigEndian.PutUint32(data[index+4+76:], 768<<16)
	binary.BigEndian.PutUint32(data[index+4+80:], 1024<<16)
	return videoWithAudioCodec(data, "aac")
}

func resourceHTTP(h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var b bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&b).Encode(body)
	}
	r := httptest.NewRequest(method, path, &b)
	r.Header.Set("Authorization", "Bearer gateway-fixture")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", uuid.NewString())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func runResourceWorker(t *testing.T, ctx context.Context, repo *postgres.TaskRepository, q *queue.RedisQueue, p core.MediaProvider, store storage.Store, id string) core.Task {
	t.Helper()
	runCtx, cancel := context.WithCancel(ctx)
	w := worker.New(repo, q, p, store, time.Millisecond, 2*time.Second, 2, time.Millisecond)
	w.OperationConcurrency = 1
	w.ArchiveConcurrency = 1
	done := make(chan error, 1)
	go func() { done <- w.Run(runCtx) }()
	var task core.Task
	var err error
	for {
		task, err = repo.Get(identity.WithOwnerID(ctx, identity.SystemOwnerID), id)
		if err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
		if task.Status == core.TaskSucceeded || task.Status == core.TaskFailed {
			break
		}
		select {
		case <-ctx.Done():
			cancel()
			<-done
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	return task
}
func TestMiniMaxPostgresFileResourceAPIAndGeneration(t *testing.T) {
	ctx, db, _, q := minimaxPostgres(t)
	owner := identity.WithOwnerID(ctx, identity.SystemOwnerID)
	repo := postgres.NewTaskRepository(db)
	resources := postgres.NewProviderResourceRepository(db)
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 320, 320))); err != nil {
		t.Fatal(err)
	}
	input := pngBytes.Bytes()
	video := videoWithAudioCodec(testmedia.MP4(120, 24, 120), "aac")
	legacyVideo := videoWithAudioCodec(testmedia.MP4(144, 24, 144), "aac")
	var uploads, generations, deletes atomic.Int32
	native := &http.Client{Transport: workerRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.minimax.io" {
			t.Fatal("wrong vendor host")
		}
		switch r.URL.Path {
		case "/v1/files/upload":
			uploadNo := uploads.Add(1)
			mr, err := r.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			_, _ = mr.NextPart()
			part, err := mr.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(part)
			fileID := strconv.Itoa(110 + int(uploadNo))
			if bytes.Equal(data, inspectedBaseVideoFixture()) {
				fileID = "210"
			} else if !bytes.Equal(data, input) {
				t.Fatal("trusted stream changed bytes")
			}
			return workerJSON(map[string]any{"file": map[string]any{"file_id": fileID, "bytes": len(data), "purpose": "video_generation_input", "created_at": time.Now().Unix()}, "base_resp": map[string]int{"status_code": 0}}), nil
		case "/v2/video_generation":
			generations.Add(1)
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			content := payload["content"].([]any)
			if item := content[1].(map[string]any); item["type"] == "video_url" {
				if item["video_url"].(map[string]any)["url"] != "mm_file://333" {
					t.Fatal("owned output binding was not reused")
				}
				return workerJSON(map[string]string{"task_id": "446"}), nil
			}
			if content[1].(map[string]any)["image_url"].(map[string]any)["url"] != "mm_file://111" {
				t.Fatal("Gateway binding did not become private provider URI")
			}
			return workerJSON(map[string]string{"task_id": "444"}), nil
		case "/v2/h3_context_ir":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			content := payload["content"].([]any)
			if content[1].(map[string]any)["video_url"].(map[string]any)["url"] != "mm_file://333" {
				t.Fatal("owned output not reused for context")
			}
			return workerJSON(map[string]string{"task_id": "666"}), nil
		case "/v2/query/video_generation/666":
			return workerJSON(map[string]any{"task": map[string]any{"id": "666", "model": "MiniMax-H3", "status": "succeeded", "task_type": "h3_context_ir", "modality": "text", "content": map[string]string{"prompt": "Prepared from an owned generated file"}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 50}}}), nil
		case "/v2/query/video_generation/446":
			return workerJSON(map[string]any{"task": map[string]any{"id": "446", "model": "MiniMax-H3", "status": "succeeded", "task_type": "generation", "modality": "video", "duration": 5, "resolution": "768P", "content": map[string]string{"url": "https://cdn.fixture/video.mp4"}, "usage": map[string]int{"output_seconds": 5, "input_seconds": 6, "input_image_count": 0}}}), nil
		case "/v2/query/video_generation/444":
			return workerJSON(map[string]any{"task": map[string]any{"id": "444", "model": "MiniMax-H3", "status": "succeeded", "task_type": "generation", "modality": "video", "duration": 5, "resolution": "768P", "content": map[string]string{"url": "https://cdn.fixture/video.mp4"}, "usage": map[string]int{"output_seconds": 5, "input_seconds": 0, "input_image_count": 1}}}), nil
		case "/v2/video_regeneration":
			generations.Add(1)
			return workerJSON(map[string]string{"task_id": "555"}), nil
		case "/v2/query/video_generation/555":
			return workerJSON(map[string]any{"task": map[string]any{"id": "555", "model": "MiniMax-H3", "status": "succeeded", "task_type": "regeneration", "modality": "video", "duration": 5, "resolution": "2K", "content": map[string]string{"url": "https://cdn.fixture/video.mp4"}, "usage": map[string]int{"output_seconds": 5, "input_seconds": 0, "input_image_count": 1}}}), nil
		case "/v1/video_generation":
			generations.Add(1)
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			id := "222"
			if payload["model"] == "S2V-01" {
				id = "223"
			}
			return workerJSON(map[string]any{"task_id": id, "base_resp": map[string]int{"status_code": 0}}), nil
		case "/v1/query/video_generation":
			id, fileID := r.URL.Query().Get("task_id"), "333"
			if id == "223" {
				fileID = "334"
			}
			return workerJSON(map[string]any{"task_id": id, "status": "Success", "file_id": fileID, "base_resp": map[string]int{"status_code": 0}}), nil
		case "/v1/files/retrieve":
			id := r.URL.Query().Get("file_id")
			purpose, typ, size := "video_generation_input", "image/png", len(input)
			if id == "333" || id == "334" {
				purpose, typ, size = "video_generation", "video/mp4", len(legacyVideo)
			}
			_ = typ
			return workerJSON(map[string]any{"file": map[string]any{"file_id": id, "bytes": size, "purpose": purpose, "download_url": "https://cdn.fixture/legacy.mp4"}, "base_resp": map[string]int{"status_code": 0}}), nil
		case "/v1/files/delete":
			deletes.Add(1)
			return workerJSON(map[string]any{"file_id": "333", "base_resp": map[string]int{"status_code": 0}}), nil
		default:
			t.Fatalf("unexpected resource upstream %s", r.URL.Path)
			return nil, nil
		}
	})}
	factory, err := minimax.Definition().Factory(providerkit.Credentials{APIKey: "resource-fixture-key", Extra: map[string]string{"file_operation_price_usd": "0.001"}}, nil, native)
	if err != nil {
		t.Fatal(err)
	}
	adapter := factory.(*minimax.Adapter)
	cdn := &http.Client{Transport: workerRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "cdn.fixture" || r.Header.Get("Authorization") != "" {
			t.Fatal("CDN got credentials")
		}
		data := video
		if r.URL.Path == "/legacy.mp4" {
			data = legacyVideo
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"video/mp4"}}, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}
	store, err := storage.NewLocal(t.TempDir(), cdn, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	library := references.New(postgres.NewReferenceRepository(db), store)
	ref, err := library.Upload(owner, "Local inspected source.png", bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	resolver := references.NewResolver(library, "signing-fixture", "https://gateway.fixture")
	p := resourceWorkerProvider{workerScope: workerScope{adapter}, opener: resolver}
	directory := resourceDirectory{p}
	service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"minimax": p}, map[string]string{"minimax": "MiniMax-H3"}, "minimax")
	service.SetAdmissionScopeResolver(pgResourceScope)
	service.EnableAccounting()
	handler := api.NewGateway(api.Dependencies{Service: service, Health: workerHealth{}, APIKey: "gateway-fixture", Storage: store, SigningSecret: "signing-fixture", MediaURLTTL: time.Hour, References: library, AssetPublicURL: "https://gateway.fixture", ResourceRepository: resources, ResourceScopes: pgResourceScope, ResourceDirectory: directory})
	base := map[string]any{"provider": "minimax", "model": minimax.ResourceUpload, "workflow_model": "MiniMax-H3", "kind": "file_resource", "subtype": "image", "name": "Owned input", "reference_ids": []string{ref.ID}}
	for _, field := range []string{"remote_id", "source_url"} {
		bad := map[string]any{}
		for k, v := range base {
			bad[k] = v
		}
		bad[field] = "opaque-not-allowed"
		got := resourceHTTP(handler, "POST", "/v1/provider-resources", bad)
		if got.Code != 400 || uploads.Load() != 0 {
			t.Fatal("untrusted resource field admitted", field, got.Code)
		}
	}
	posted := resourceHTTP(handler, "POST", "/v1/provider-resources", base)
	if posted.Code != 202 {
		t.Fatalf("resource admission %d:%s", posted.Code, posted.Body.String())
	}
	var accepted struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(posted.Body.Bytes(), &accepted)
	uploaded := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
	if uploaded.Status != core.TaskSucceeded || uploads.Load() != 1 || len(uploaded.StoredResourceReferences) != 1 {
		t.Fatal("resource upload failed", uploaded.Error, uploads.Load(), len(uploaded.StoredResourceReferences))
	}
	binding := uploaded.StoredResourceReferences[0].BindingID
	view := resourceHTTP(handler, "GET", "/v1/provider-resources/"+binding, nil)
	if view.Code != 200 || strings.Contains(view.Body.String(), "minimax:") || strings.Contains(view.Body.String(), "remote_id") || strings.Contains(view.Body.String(), "source_url") {
		t.Fatal("private file binding leaked", view.Code)
	}
	generate := resourceHTTP(handler, "POST", "/v1/videos", map[string]any{"model": "minimax/MiniMax-H3", "prompt": "Use @image1", "options": map[string]any{"reference_mode": "references", "resource_references": []map[string]string{{"binding_id": binding, "reference_id": "image_1"}}}})
	if generate.Code != 202 {
		t.Fatalf("managed file generation admission %d:%s", generate.Code, generate.Body.String())
	}
	_ = json.Unmarshal(generate.Body.Bytes(), &accepted)
	generated := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
	if generated.Status != core.TaskSucceeded || generations.Load() != 1 {
		t.Fatal("managed file generation failed", generated.Error)
	}

	// Public source-task regeneration resolves local archived task -> opaque binding.
	sourceBound := resourceHTTP(handler, "POST", "/v1/provider-resources", map[string]any{"provider": "minimax", "model": minimax.ResourceSourceTask, "workflow_model": minimax.Regeneration, "kind": "file_resource", "subtype": "regeneration_source", "name": "Owned H3 source", "generated_task_id": generated.ID})
	if sourceBound.Code != 202 {
		t.Fatalf("H3 source binding %d:%s", sourceBound.Code, sourceBound.Body.String())
	}
	_ = json.Unmarshal(sourceBound.Body.Bytes(), &accepted)
	sourceTask := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
	if sourceTask.Status != core.TaskSucceeded || len(sourceTask.StoredResourceReferences) != 1 {
		t.Fatal("H3 source binding failed", sourceTask.Error)
	}
	regen := resourceHTTP(handler, "POST", "/v1/videos", map[string]any{"model": "minimax/" + minimax.Regeneration, "options": map[string]any{"resource_references": []map[string]string{{"binding_id": sourceTask.StoredResourceReferences[0].BindingID, "reference_id": "source_task"}}}})
	if regen.Code != 202 {
		t.Fatalf("bound regeneration %d:%s", regen.Code, regen.Body.String())
	}
	_ = json.Unmarshal(regen.Body.Bytes(), &accepted)
	regenerated := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
	if regenerated.Status != core.TaskSucceeded || regenerated.Cost == nil || regenerated.Cost.MeteredUSD == nil || *regenerated.Cost.MeteredUSD != 250000 {
		t.Fatal("regeneration binding/accounting failed", regenerated.Error)
	}
	public := resourceHTTP(handler, "GET", "/v1/generations/"+regenerated.ID, nil)
	if public.Code != 200 || strings.Contains(public.Body.String(), "minimax:") || strings.Contains(public.Body.String(), "source_task_id") {
		t.Fatal("regeneration source handle exposed")
	}
	oldShape := resourceHTTP(handler, "POST", "/v1/videos", map[string]any{"model": "minimax/" + minimax.Regeneration, "options": map[string]any{"parameters": map[string]any{"source_task": "raw-provider-task"}}})
	if oldShape.Code < 400 {
		t.Fatal("raw source handle accepted")
	}

	for _, mode := range []string{"first_frame", "last_frame", "first_last_frame"} {
		t.Run("managed file "+mode, func(t *testing.T) {
			refs := []map[string]string{{"binding_id": binding, "reference_id": "image_1"}}
			if mode == "first_last_frame" {
				second := resourceHTTP(handler, "POST", "/v1/provider-resources", map[string]any{"provider": "minimax", "model": minimax.ResourceUpload, "workflow_model": "MiniMax-H3", "kind": "file_resource", "subtype": "image", "name": "Owned last frame", "reference_ids": []string{ref.ID}})
				if second.Code != 202 {
					t.Fatalf("second frame binding admission %d", second.Code)
				}
				_ = json.Unmarshal(second.Body.Bytes(), &accepted)
				secondTask := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
				if secondTask.Status != core.TaskSucceeded || len(secondTask.StoredResourceReferences) != 1 {
					t.Fatal("second frame binding failed", secondTask.Error)
				}
				refs = append(refs, map[string]string{"binding_id": secondTask.StoredResourceReferences[0].BindingID, "reference_id": "image_2"})
			}
			framed := resourceHTTP(handler, "POST", "/v1/videos", map[string]any{"model": "minimax/MiniMax-H3", "prompt": "Owned frame sequence", "options": map[string]any{"reference_mode": mode, "resource_references": refs}})
			if framed.Code != 202 {
				t.Fatalf("managed frame admission %d:%s", framed.Code, framed.Body.String())
			}
			_ = json.Unmarshal(framed.Body.Bytes(), &accepted)
			framedTask := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
			if framedTask.Status != core.TaskSucceeded || len(framedTask.StoredArtifacts) != 1 {
				t.Fatal("managed frame did not archive", framedTask.Error)
			}
		})
	}

	t.Run("standalone inspected source", func(t *testing.T) {
		// Standalone regeneration accepts a locally inspected conforming video, without a source task.
		baseBytes := inspectedBaseVideoFixture()
		baseRef, e := library.Upload(owner, "Inspected H3 output.mp4", bytes.NewReader(baseBytes))
		if e != nil {
			t.Fatal("base video fixture could not be inspected", e)
		}
		baseURL, e := references.CanonicalReferenceURL("https://gateway.fixture", []byte("signing-fixture"), identity.SystemOwnerID, baseRef.ID)
		if e != nil {
			t.Fatal(e)
		}
		standalone := resourceHTTP(handler, "POST", "/v1/videos", map[string]any{"model": "minimax/" + minimax.RegenerationVideo, "prompt": "The exact original final scene prompt", "options": map[string]any{"reference_mode": "references", "video_urls": []string{baseURL}, "reference_roles": map[string]string{"video_1": "base_video"}}})
		if standalone.Code != 202 {
			t.Fatalf("inspected standalone admission %d:%s", standalone.Code, standalone.Body.String())
		}
		_ = json.Unmarshal(standalone.Body.Bytes(), &accepted)
		standaloneTask := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
		if standaloneTask.Status != core.TaskSucceeded || standaloneTask.Cost == nil || standaloneTask.Cost.MeteredUSD == nil || *standaloneTask.Cost.MeteredUSD != 250000 {
			t.Fatal("standalone regeneration did not archive/meter", standaloneTask.Error)
		}
	})

	t.Run("file bound inspected base evidence", func(t *testing.T) {
		baseRef, err := library.Upload(owner, "Bound inspected source.mp4", bytes.NewReader(inspectedBaseVideoFixture()))
		if err != nil {
			t.Fatal(err)
		}
		posted := resourceHTTP(handler, "POST", "/v1/provider-resources", map[string]any{"provider": "minimax", "model": minimax.ResourceUpload, "workflow_model": minimax.RegenerationVideo, "kind": "file_resource", "subtype": "video", "name": "Owned inspected base", "reference_ids": []string{baseRef.ID}})
		if posted.Code != 202 {
			t.Fatalf("base upload admission %d:%s", posted.Code, posted.Body.String())
		}
		_ = json.Unmarshal(posted.Body.Bytes(), &accepted)
		bound := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
		if bound.Status != core.TaskSucceeded || len(bound.StoredResourceReferences) != 1 {
			t.Fatal("base file binding failed", bound.Error)
		}
		regenerated := resourceHTTP(handler, "POST", "/v1/videos", map[string]any{"model": "minimax/" + minimax.RegenerationVideo, "prompt": "The exact original final scene prompt", "options": map[string]any{"reference_mode": "references", "resource_references": []map[string]string{{"binding_id": bound.StoredResourceReferences[0].BindingID, "reference_id": "video_1"}}, "reference_roles": map[string]string{"video_1": "base_video"}}})
		if regenerated.Code != 202 {
			t.Fatalf("persisted inspected base evidence must support regeneration: status=%d body=%s", regenerated.Code, regenerated.Body.String())
		}
		_ = json.Unmarshal(regenerated.Body.Bytes(), &accepted)
		result := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
		if result.Status != core.TaskSucceeded || len(result.StoredArtifacts) != 1 {
			t.Fatal("file bound base did not archive", result.Error)
		}
	})

	// Input deletion remains intentionally unavailable under the official purpose enum.
	denied := resourceHTTP(handler, "DELETE", "/v1/provider-resources/"+binding, nil)
	if denied.Code != http.StatusMethodNotAllowed || deletes.Load() != 0 {
		t.Errorf("undocumented input deletion must reject before lifecycle mutation: status=%d native_deletes=%d", denied.Code, deletes.Load())
	}
	stillBound := resourceHTTP(handler, "GET", "/v1/provider-resources/"+binding, nil)
	var usable struct {
		Usable bool `json:"usable"`
	}
	if err := json.Unmarshal(stillBound.Body.Bytes(), &usable); err != nil || stillBound.Code != 200 || !usable.Usable {
		t.Errorf("rejected input deletion must preserve a usable owned binding: status=%d usable=%t", stillBound.Code, usable.Usable)
	}
	legacy := resourceHTTP(handler, "POST", "/v1/videos", map[string]any{"model": "minimax/MiniMax-Hailuo-2.3", "prompt": "Owned legacy output"})
	if legacy.Code != 202 {
		t.Fatal(legacy.Code)
	}
	_ = json.Unmarshal(legacy.Body.Bytes(), &accepted)
	legacyTask := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
	if legacyTask.Status != core.TaskSucceeded {
		t.Fatal(legacyTask.Error)
	}
	output := resourceHTTP(handler, "POST", "/v1/provider-resources", map[string]any{"provider": "minimax", "model": minimax.ResourceOutputFile, "workflow_model": "MiniMax-H3", "kind": "file_resource", "subtype": "video", "name": "Owned output", "generated_task_id": legacyTask.ID})
	if output.Code != 202 {
		t.Fatalf("owned output bind %d:%s", output.Code, output.Body.String())
	}
	_ = json.Unmarshal(output.Body.Bytes(), &accepted)
	bound := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
	if bound.Status != core.TaskSucceeded || len(bound.StoredResourceReferences) != 1 {
		t.Fatal("output binding failed", bound.Error)
	}
	for _, model := range []string{"MiniMax-H3", minimax.ContextIR} {
		t.Run("owned output reused by "+model, func(t *testing.T) {
			path := "/v1/videos"
			if model == minimax.ContextIR {
				path = "/v1/generations"
			}
			continued := resourceHTTP(handler, "POST", path, map[string]any{"model": "minimax/" + model, "prompt": "Continue @video1", "options": map[string]any{"reference_mode": "references", "resource_references": []map[string]string{{"binding_id": bound.StoredResourceReferences[0].BindingID, "reference_id": "video_1"}}}})
			if model == minimax.ContextIR {
				if continued.Code != 400 {
					t.Fatalf("retired ContextIR reuse admitted: %d", continued.Code)
				}
				return
			}
			if continued.Code != 202 {
				t.Fatalf("owned output admission %d:%s", continued.Code, continued.Body.String())
			}
			_ = json.Unmarshal(continued.Body.Bytes(), &accepted)
			continuedTask := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
			metered := int64(880000)
			if model == minimax.ContextIR {
				metered = 270
			}
			if continuedTask.Status != core.TaskSucceeded || len(continuedTask.StoredArtifacts) != 1 || continuedTask.Cost == nil || continuedTask.Cost.MeteredUSD == nil || int64(*continuedTask.Cost.MeteredUSD) != metered {
				t.Fatal("owned output reuse did not archive/meter", continuedTask.Error)
			}
		})
	}
	deleted := resourceHTTP(handler, "DELETE", "/v1/provider-resources/"+bound.StoredResourceReferences[0].BindingID, nil)
	if deleted.Code != http.StatusMethodNotAllowed || deletes.Load() != 0 {
		t.Fatalf("standalone deletion must be unavailable without native mutation: status=%d calls=%d", deleted.Code, deletes.Load())
	}
	retained, readErr := resources.GetBinding(owner, bound.StoredResourceReferences[0].BindingID)
	if readErr != nil || retained.Status != "succeeded" {
		t.Fatal("retired deletion changed owned output", readErr)
	}
	if _, err = resources.GetBinding(identity.WithOwnerID(ctx, uuid.NewString()), binding); err == nil {
		t.Fatal("foreign resource binding read")
	}
	t.Run("unknown duration owned output binding", func(t *testing.T) {
		// S2V has no official tariff. Disable accounting only for this generation
		// fixture; bind it through the ordinary priced resource API afterward.
		unpricedService := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"minimax": p}, map[string]string{"minimax": "S2V-01"}, "minimax")
		unpricedService.SetAdmissionScopeResolver(pgResourceScope)
		unpricedHandler := api.NewGateway(api.Dependencies{Service: unpricedService, Health: workerHealth{}, APIKey: "gateway-fixture", Storage: store, SigningSecret: "signing-fixture", MediaURLTTL: time.Hour, References: library, AssetPublicURL: "https://gateway.fixture", ResourceRepository: resources, ResourceScopes: pgResourceScope, ResourceDirectory: directory})
		portraitURL, err := references.CanonicalReferenceURL("https://gateway.fixture", []byte("signing-fixture"), identity.SystemOwnerID, ref.ID)
		if err != nil {
			t.Fatal(err)
		}
		posted := resourceHTTP(unpricedHandler, "POST", "/v1/videos", map[string]any{"model": "minimax/S2V-01", "prompt": "Owned portrait", "input_reference": []string{portraitURL}, "options": map[string]any{"reference_mode": "references"}})
		if posted.Code != 202 {
			t.Fatalf("unknown tariff fixture generation admission %d:%s", posted.Code, posted.Body.String())
		}
		_ = json.Unmarshal(posted.Body.Bytes(), &accepted)
		generated := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
		if generated.Status != core.TaskSucceeded || len(generated.StoredArtifacts) != 1 || generated.StoredArtifacts[0].Duration != 0 {
			t.Fatal("unknown duration must not be fabricated", generated.Error)
		}
		if generated.ProviderIdentity == "" {
			t.Error("unpriced generation must persist canonical provider connection revision independently of a successful cost quote")
		}
		posted = resourceHTTP(handler, "POST", "/v1/provider-resources", map[string]any{"provider": "minimax", "model": minimax.ResourceOutputFile, "workflow_model": "MiniMax-H3", "kind": "file_resource", "subtype": "video", "name": "Owned duration unknown", "generated_task_id": generated.ID})
		if posted.Code != 202 {
			t.Fatalf("unknown duration owned output must be bindable without fabrication: status=%d body=%s", posted.Code, posted.Body.String())
		}
		_ = json.Unmarshal(posted.Body.Bytes(), &accepted)
		bound := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
		if bound.Status != core.TaskSucceeded || len(bound.StoredResourceReferences) != 1 {
			t.Fatal("unknown duration output binding failed", bound.Error)
		}
		binding, err := resources.GetBinding(owner, bound.StoredResourceReferences[0].BindingID)
		if err != nil || len(binding.Sources) != 1 || binding.Sources[0].Duration != nil {
			t.Fatal("unknown duration must persist as nil", err)
		}
		before := generations.Load()
		attempt := resourceHTTP(handler, "POST", "/v1/videos", map[string]any{"model": "minimax/MiniMax-H3", "prompt": "Use @video1", "options": map[string]any{"reference_mode": "references", "resource_references": []map[string]string{{"binding_id": binding.ID, "reference_id": "video_1"}}}})
		if attempt.Code < 400 || generations.Load() != before {
			t.Fatal("unknown input duration became a guaranteed free quantity", attempt.Code)
		}
	})

}

// The fixture is independently encoded locally; no image bytes are committed.
func TestMiniMaxPostgresHEIFResourceRoundTrip(t *testing.T) {
	path := os.Getenv("GATEWAY_TEST_HEIC_FIXTURE")
	if path == "" {
		t.Skip("requires an independently encoded local HEIC fixture")
	}
	for i, fixture := range filepath.SplitList(path) {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			data, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			runManagedMediaResourceRoundTrip(t, data, "Inspected still.heif", "image", "first_frame", "first_frame")
		})
	}
}

func TestMiniMaxPostgresWAVResourceRoundTrip(t *testing.T) {
	data := testmedia.WAV()
	data = append(data, make([]byte, 8000)...)
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	binary.LittleEndian.PutUint32(data[40:44], 16000)
	runManagedMediaResourceRoundTrip(t, data, "Inspected sound.wav", "audio", "references", "reference_audio")
}

func runManagedMediaResourceRoundTrip(t *testing.T, data []byte, name, kind, mode, role string) {
	ctx, db, dsn, q := minimaxPostgres(t)
	owner := identity.WithOwnerID(ctx, identity.SystemOwnerID)
	repo := postgres.NewTaskRepository(db)
	resources := postgres.NewProviderResourceRepository(db)
	var submissions atomic.Int32
	video := append([]byte{0, 0, 0, 20, 'f', 't', 'y', 'p'}, []byte("isom0000isom")...)
	native := &http.Client{Transport: workerRoundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/files/upload":
			mr, err := r.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			_, _ = mr.NextPart()
			part, err := mr.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(part)
			if !bytes.Equal(got, data) {
				t.Fatal("Managed media trusted stream changed inspected bytes")
			}
			return workerJSON(map[string]any{"file": map[string]any{"file_id": "987", "bytes": len(data), "purpose": "video_generation_input", "created_at": time.Now().Unix()}, "base_resp": map[string]int{"status_code": 0}}), nil
		case "/v2/video_generation":
			submissions.Add(1)
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			content := payload["content"].([]any)
			url := content[1].(map[string]any)[kind+"_url"].(map[string]any)["url"].(string)
			if (url != "mm_file://987" && !(kind == "video" && strings.HasPrefix(url, "https://gateway.fixture/"))) || content[1].(map[string]any)["role"] != role {
				t.Fatal("Managed media binding did not become an owned first frame")
			}
			return workerJSON(map[string]string{"task_id": "123"}), nil
		case "/v2/query/video_generation/123":
			return workerJSON(map[string]any{"task": map[string]any{"id": "123", "model": "MiniMax-H3", "status": "succeeded", "task_type": "generation", "modality": "video", "resolution": "768P", "duration": 5, "content": map[string]string{"url": "https://cdn.fixture/video.mp4"}, "usage": map[string]int{"output_seconds": 5, "input_seconds": map[string]int{"video": 5}[kind], "input_image_count": map[string]int{"image": 1}[kind]}}}), nil
		default:
			t.Fatal("unexpected Managed media provider route", r.URL.Path)
			return nil, nil
		}
	})}
	provider, err := minimax.Definition().Factory(providerkit.Credentials{APIKey: "heif-fixture", Extra: map[string]string{"file_operation_price_usd": "0.001"}}, nil, native)
	if err != nil {
		t.Fatal(err)
	}
	cdn := &http.Client{Transport: workerRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "cdn.fixture" || r.Header.Get("Authorization") != "" {
			t.Fatal("Managed media archive host or credential leak")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"video/mp4"}}, ContentLength: int64(len(video)), Body: io.NopCloser(bytes.NewReader(video))}, nil
	})}
	root := t.TempDir()
	store, err := storage.NewLocal(root, cdn, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	library := references.New(postgres.NewReferenceRepository(db), store)
	ref, err := library.Upload(owner, name, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := library.Inspect(owner, ref.ID)
	if err != nil || inspected.Type != kind || kind == "image" && (inspected.Info.Width < 256 || inspected.Info.Height < 256) || kind == "audio" && inspected.Info.Duration != 2 {
		t.Fatal("Managed media inspection did not prove H3 bounds", err)
	}
	p := resourceWorkerProvider{workerScope: workerScope{provider.(*minimax.Adapter)}, opener: references.NewResolver(library, "signing-fixture", "https://gateway.fixture")}
	service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"minimax": p}, map[string]string{"minimax": "MiniMax-H3"}, "minimax")
	service.SetAdmissionScopeResolver(pgResourceScope)
	service.EnableAccounting()
	handler := api.NewGateway(api.Dependencies{Service: service, Health: workerHealth{}, APIKey: "gateway-fixture", Storage: store, SigningSecret: "signing-fixture", MediaURLTTL: time.Hour, References: library, AssetPublicURL: "https://gateway.fixture", ResourceRepository: resources, ResourceScopes: pgResourceScope, ResourceDirectory: resourceDirectory{p}})
	if kind == "video" {
		allowedCodec := core.AudioCodecsAllowed(inspected.Info.AudioStreams, inspected.Info.AudioCodecs, []string{"aac", "mp3"})
		url, err := references.CanonicalReferenceURL("https://gateway.fixture", []byte("signing-fixture"), identity.SystemOwnerID, ref.ID)
		if err != nil {
			t.Fatal(err)
		}
		direct := resourceHTTP(handler, "POST", "/v1/videos", map[string]any{"model": "minimax/MiniMax-H3", "prompt": "Follow the inspected reference", "options": map[string]any{"reference_mode": "references", "video_urls": []string{url}}})
		if !allowedCodec {
			if direct.Code < 400 {
				t.Fatal("unproved direct audio codec admitted", direct.Code)
			}
			upload := resourceHTTP(handler, "POST", "/v1/provider-resources", map[string]any{"provider": "minimax", "model": minimax.ResourceUpload, "workflow_model": "MiniMax-H3", "kind": "file_resource", "subtype": "video", "name": "Reject unproved audio codec", "reference_ids": []string{ref.ID}})
			if upload.Code < 400 || submissions.Load() != 0 {
				t.Fatal("unproved upload codec admitted or submitted", upload.Code, submissions.Load())
			}
			var tasks int
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM tasks").Scan(&tasks); err != nil || tasks != 0 {
				t.Fatal("invalid codec reserved task", tasks, err)
			}
			return
		}
		if direct.Code != 202 {
			t.Fatalf("direct inspected codec admission %d:%s", direct.Code, direct.Body.String())
		}
		var queued struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(direct.Body.Bytes(), &queued)
		if result := runResourceWorker(t, ctx, repo, q, p, store, queued.ID); result.Status != core.TaskSucceeded {
			t.Fatal("direct inspected codec dispatch", result.Error)
		}
	}
	posted := resourceHTTP(handler, "POST", "/v1/provider-resources", map[string]any{"provider": "minimax", "model": minimax.ResourceUpload, "workflow_model": "MiniMax-H3", "kind": "file_resource", "subtype": kind, "name": "Owned inspected media", "reference_ids": []string{ref.ID}})
	if posted.Code != 202 {
		t.Fatalf("Managed media public upload %d:%s", posted.Code, posted.Body.String())
	}
	var accepted struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(posted.Body.Bytes(), &accepted)
	bound := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
	if bound.Status != core.TaskSucceeded || len(bound.StoredResourceReferences) != 1 {
		message := "missing task error"
		if bound.Error != nil {
			message = *bound.Error
		}
		t.Fatal("Managed media upload did not bind", message)
	}
	posted = resourceHTTP(handler, "POST", "/v1/videos", map[string]any{"model": "minimax/MiniMax-H3", "prompt": "Owned Managed media frame", "options": map[string]any{"reference_mode": mode, "resource_references": []map[string]string{{"binding_id": bound.StoredResourceReferences[0].BindingID, "reference_id": kind + "_1"}}}})
	if posted.Code != 202 {
		t.Fatalf("Managed media first frame admission %d:%s", posted.Code, posted.Body.String())
	}
	_ = json.Unmarshal(posted.Body.Bytes(), &accepted)
	result := runResourceWorker(t, ctx, repo, q, p, store, accepted.ID)
	if result.Status != core.TaskSucceeded || len(result.StoredArtifacts) != 1 {
		t.Fatal("Managed media based output did not archive", result.Error)
	}
	reopened, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := postgres.NewProviderResourceRepository(reopened).GetBinding(owner, bound.StoredResourceReferences[0].BindingID)
	if err != nil || len(persisted.Sources) != 1 || persisted.Sources[0].Inspection == nil || persisted.Sources[0].Inspection.Width != inspected.Info.Width || persisted.Sources[0].Inspection.Height != inspected.Info.Height || persisted.Sources[0].SHA256 != inspected.SHA256 {
		t.Fatal("Managed media inspected proof did not survive reopen", err)
	}
	if kind == "video" {
		proof := persisted.Sources[0].Inspection
		if proof.AudioStreams == nil || inspected.Info.AudioStreams == nil || *proof.AudioStreams != *inspected.Info.AudioStreams || !reflect.DeepEqual(proof.AudioCodecs, inspected.Info.AudioCodecs) {
			t.Fatal("codec proof lost on reopen")
		}
		if _, err := postgres.NewProviderResourceRepository(reopened).GetBinding(identity.WithOwnerID(ctx, uuid.NewString()), persisted.ID); err == nil {
			t.Fatal("foreign owner read codec binding")
		}
		// Reproduce malformed historical stored evidence only in this private
		// disposable schema; public callers cannot write these proof fields.
		original, err := json.Marshal(persisted.Sources)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name   string
			count  *int
			codecs []string
		}{
			{"unsupported", func() *int { n := 1; return &n }(), []string{"opus"}},
			{"present unknown", func() *int { n := 1; return &n }(), []string{""}},
			{"unknown count", nil, []string{"aac"}},
			{"count mismatch", func() *int { n := 2; return &n }(), []string{"aac"}},
		} {
			bad := core.CloneResourceSources(persisted.Sources)
			bad[0].Inspection.AudioStreams = tc.count
			bad[0].Inspection.AudioCodecs = tc.codecs
			raw, _ := json.Marshal(bad)
			if _, err := reopened.ExecContext(ctx, "UPDATE provider_resources SET sources=$2 WHERE id=$1", persisted.ID, raw); err != nil {
				t.Fatal(err)
			}
			before := submissions.Load()
			rejected := resourceHTTP(handler, "POST", "/v1/videos", map[string]any{"model": "minimax/MiniMax-H3", "prompt": "Reject unproved codec", "options": map[string]any{"reference_mode": "references", "resource_references": []map[string]string{{"binding_id": persisted.ID, "reference_id": "video_1"}}}})
			if rejected.Code < 400 || submissions.Load() != before {
				t.Fatal("bad persisted codec must reject before submission", tc.name, rejected.Code)
			}
		}
		if _, err := reopened.ExecContext(ctx, "UPDATE provider_resources SET sources=$2 WHERE id=$1", persisted.ID, original); err != nil {
			t.Fatal(err)
		}
	}
	restartedService := core.NewTaskServiceWithProviders(postgres.NewTaskRepository(reopened), q, map[string]core.MediaProvider{"minimax": p}, map[string]string{"minimax": "MiniMax-H3"}, "minimax")
	restarted := api.NewWithStorage(restartedService, workerHealth{}, "gateway-fixture", store, "signing-fixture", time.Hour)
	view := workerRequest(restarted, "GET", "/v1/generations/"+result.ID, nil, true)
	if view.Code != 200 || strings.Contains(view.Body.String(), "minimax:") || strings.Contains(view.Body.String(), "mm_file:") || strings.Contains(view.Body.String(), "cdn.fixture") {
		t.Fatal("Managed media output view missing or unredacted")
	}
	var public struct {
		ContentURL string `json:"content_url"`
	}
	_ = json.Unmarshal(view.Body.Bytes(), &public)
	for _, method := range []string{"GET", "HEAD"} {
		got := workerRequest(restarted, method, public.ContentURL, nil, true)
		if got.Code != 200 || method == "GET" && !bytes.Equal(got.Body.Bytes(), video) {
			t.Fatal("Managed media derived archived content unavailable", method, got.Code)
		}
	}
}

// Self-contained audio samples and decoder descriptors exercise actual byte
// inspection. AAC identity comes from ESDS+ASC; MP3 includes LayerIII frames.
func videoWithAudioCodec(video []byte, codec string) []byte {
	if codec == "silent" {
		return bytes.Clone(video)
	}
	prefix := int(binary.BigEndian.Uint32(video[:4]))
	rate, oti := uint32(48000), byte(0x40)
	config, sample := []byte{0x11, 0x90}, []byte{0x21, 0x10}
	if codec == "mp3" {
		rate, oti, config = 44100, 0x6b, nil
		sample = testmedia.MP3()
	}
	if codec == "unknown" {
		config = nil
	}
	descriptor := func(tag byte, payload []byte) []byte { return append([]byte{tag, byte(len(payload))}, payload...) }
	decoder := make([]byte, 13)
	decoder[0], decoder[1] = oti, 0x15
	if len(config) > 0 {
		decoder = append(decoder, descriptor(5, config)...)
	}
	es := append([]byte{0, 1, 0}, descriptor(4, decoder)...)
	es = append(es, descriptor(6, []byte{2})...)
	entry := make([]byte, 28)
	binary.BigEndian.PutUint16(entry[6:], 1)
	binary.BigEndian.PutUint16(entry[16:], 2)
	binary.BigEndian.PutUint16(entry[18:], 16)
	binary.BigEndian.PutUint32(entry[24:], rate<<16)
	entry = append(entry, testmedia.Atom("esds", append(make([]byte, 4), descriptor(3, es)...))...)
	tag := "mp4a"
	if codec == "opus" {
		tag = "Opus"
	}
	stsd := make([]byte, 8)
	binary.BigEndian.PutUint32(stsd[4:], 1)
	stsd = append(stsd, testmedia.Atom(tag, entry)...)
	stts := make([]byte, 16)
	binary.BigEndian.PutUint32(stts[4:], 1)
	binary.BigEndian.PutUint32(stts[8:], 3)
	binary.BigEndian.PutUint32(stts[12:], 1024)
	stsz := make([]byte, 12)
	binary.BigEndian.PutUint32(stsz[4:], uint32(len(sample)))
	binary.BigEndian.PutUint32(stsz[8:], 3)
	stsc := make([]byte, 20)
	for _, off := range []int{4, 8, 16} {
		binary.BigEndian.PutUint32(stsc[off:], 1)
	}
	binary.BigEndian.PutUint32(stsc[12:], 3)
	stco := make([]byte, 12)
	binary.BigEndian.PutUint32(stco[4:], 1)
	binary.BigEndian.PutUint32(stco[8:], uint32(prefix+8))
	stbl := append(testmedia.Atom("stsd", stsd), testmedia.Atom("stts", stts)...)
	for _, box := range [][]byte{testmedia.Atom("stsz", stsz), testmedia.Atom("stsc", stsc), testmedia.Atom("stco", stco)} {
		stbl = append(stbl, box...)
	}
	dref := make([]byte, 8)
	binary.BigEndian.PutUint32(dref[4:], 1)
	dref = append(dref, testmedia.Atom("url ", []byte{0, 0, 0, 1})...)
	minf := append(testmedia.Atom("dinf", testmedia.Atom("dref", dref)), testmedia.Atom("stbl", stbl)...)
	hdlr := make([]byte, 24)
	copy(hdlr[8:], "soun")
	mdhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mdhd[12:], rate)
	binary.BigEndian.PutUint32(mdhd[16:], 3072)
	mdia := append(testmedia.Atom("hdlr", hdlr), testmedia.Atom("mdhd", mdhd)...)
	mdia = append(mdia, testmedia.Atom("minf", minf)...)
	track := testmedia.Atom("trak", append(testmedia.Atom("tkhd", make([]byte, 84)), testmedia.Atom("mdia", mdia)...))
	movie := append(bytes.Clone(video[prefix+8:]), track...)
	out := append(bytes.Clone(video[:prefix]), testmedia.Atom("mdat", bytes.Repeat(sample, 3))...)
	return append(out, testmedia.Atom("moov", movie)...)
}

func TestMiniMaxPostgresEmbeddedAudioReferenceVideos(t *testing.T) {
	for _, codec := range []string{"aac", "mp3", "silent"} {
		t.Run(codec, func(t *testing.T) {
			runManagedMediaResourceRoundTrip(t, videoWithAudioCodec(testmedia.MP4(120, 24, 120), codec), "Inspected reference.mp4", "video", "references", "reference_video")
		})
	}
}

func TestMiniMaxPostgresUnprovedEmbeddedAudioRejectsBeforeAdmission(t *testing.T) {
	for _, codec := range []string{"unknown", "opus", "unknown inventory"} {
		t.Run(codec, func(t *testing.T) {
			data := videoWithAudioCodec(testmedia.MP4(120, 24, 120), codec)
			if codec == "unknown inventory" {
				at := bytes.Index(data, []byte("moov")) - 4
				extra := testmedia.Atom("trak", testmedia.Atom("mdia", nil))
				binary.BigEndian.PutUint32(data[at:], binary.BigEndian.Uint32(data[at:])+uint32(len(extra)))
				data = append(data, extra...)
			}
			runManagedMediaResourceRoundTrip(t, data, "Unproved reference.mp4", "video", "references", "reference_video")
		})
	}
}
func TestMiniMaxPostgresLocalEncodedReferenceAudioCodecs(t *testing.T) {
	dir := os.Getenv("GATEWAY_AUDIO_CODEC_FIXTURES")
	if dir == "" {
		t.Skip("requires independent local encoder fixtures")
	}
	for _, codec := range []string{"aac", "mp3"} {
		t.Run(codec, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, codec+".mp4"))
			if err != nil {
				t.Fatal(err)
			}
			runManagedMediaResourceRoundTrip(t, data, "Encoded reference.mp4", "video", "references", "reference_video")
		})
	}
}
