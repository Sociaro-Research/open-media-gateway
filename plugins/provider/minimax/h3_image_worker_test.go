package minimax_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/worker"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/minimax"
)

// Interrupt the first process's archival read only after the real receipt or
// its first artifact checkpoint committed. The next worker opens a new DB pool.
type h3InterruptedStore struct {
	*storage.Local
	calls, before int
	stop          context.CancelFunc
}

func (s *h3InterruptedStore) Archive(ctx context.Context, id, src string) (core.StoredMedia, error) {
	s.calls++
	if s.calls == s.before {
		s.stop()
		<-ctx.Done()
		return core.StoredMedia{}, ctx.Err()
	}
	return s.Local.Archive(ctx, id, src)
}

func (s *h3InterruptedStore) ArchiveSource(ctx context.Context, id string, src core.ArtifactSource) (core.StoredMedia, error) {
	s.calls++
	if s.calls == s.before {
		s.stop()
		<-ctx.Done()
		src.Reader.Close()
		return core.StoredMedia{}, ctx.Err()
	}
	return s.Local.ArchiveSource(ctx, id, src)
}

func TestH3ImageWorkerArchiveUsageRestart(t *testing.T) {
	for _, model := range []string{"MiniMax-H3-Image", "MiniMax-H3-Image-Highspeed"} {
		modes := []string{"generation", "edit"}
		if model == "MiniMax-H3-Image-Highspeed" {
			modes = append(modes, "edit missing category")
		}
		for _, scenario := range modes {
			mode := scenario
			missing := scenario == "edit missing category"
			if missing {
				mode = "edit"
			}
			t.Run(model+"/"+scenario, func(t *testing.T) {
				ctx, db, dsn, q := minimaxPostgres(t)
				const revision = "h3-image-fixture"
				owner := identity.WithOwnerID(ctx, identity.SystemOwnerID)
				scope := core.WithProviderConnectionScope(owner, core.ProviderConnectionScope{ID: "minimax", OwnerID: identity.SystemOwnerID, Revision: revision})

				inline := model == "MiniMax-H3-Image-Highspeed"
				var posts, fetches atomic.Int32
				var encoded bytes.Buffer
				if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 1024, 1024))); err != nil {
					t.Fatal(err)
				}
				png := encoded.Bytes()
				expected := map[string]any{"model": model, "prompt": "fixture", "n": float64(3), "stream": false, "output_format": "png", "area": "1K"}
				path := "/v1/images/generations"
				options := core.MediaOptions{OutputCount: 3, Format: "png", Parameters: map[string]any{"area": "1K"}}
				inputImage := 0
				if mode == "edit" {
					inputImage = 2000
					path = "/v1/images/edits"
					ref := "https://reference.fixture/input.png"
					options.ImageURLs = []string{ref}
					options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{ref: {Type: "image", ContentType: "image/png", SizeBytes: 520, Width: 1024, Height: 1024}}
					expected["images"] = []any{map[string]any{"image_url": ref}}
				} else {
					options.Parameters["disable_prompt_enhanced"] = false
					expected["disable_prompt_enhanced"] = false
				}
				if inline {
					options.Parameters["response_format"] = "b64_json"
					expected["response_format"] = "b64_json"
				}
				resultItem := func(i int) any {
					v := map[string]any{"size": "1024x1024", "revised_prompt": "private enhanced wording"}
					if inline {
						v["b64_json"] = base64.StdEncoding.EncodeToString(png)
					} else {
						v["url"] = fmt.Sprintf("https://media.fixture/%d.png?signature=private", i)
					}
					return v
				}
				mediaClient := &http.Client{Transport: workerRoundTrip(func(r *http.Request) (*http.Response, error) {
					if r.Method != "GET" || r.URL.Host != "media.fixture" || r.Header.Get("Authorization") != "" {
						return nil, errors.New("fixture archive destination mismatch")
					}
					fetches.Add(1)
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, ContentLength: int64(len(png)), Body: io.NopCloser(bytes.NewReader(png))}, nil
				})}
				native := &http.Client{Transport: workerRoundTrip(func(req *http.Request) (*http.Response, error) {
					if req.Method == "GET" {
						return mediaClient.Transport.RoundTrip(req)
					}
					if posts.Add(1) != 1 || req.Method != "POST" || req.URL.Host != "api.minimax.io" || req.URL.Path != path || req.GetBody != nil {
						t.Error("unexpected native submission")
						return nil, errors.New("fixture submission mismatch")
					}
					var got map[string]any
					if err := json.NewDecoder(req.Body).Decode(&got); err != nil || !reflect.DeepEqual(got, expected) {
						t.Error("incorrect native serializer")
						return nil, errors.New("fixture payload mismatch")
					}
					input := map[string]int{"text_tokens": 1000, "image_tokens": inputImage}
					if missing {
						delete(input, "image_tokens")
					}
					usage := map[string]any{"generated_images": 2, "input_tokens": 1000 + inputImage, "output_tokens": 3000, "total_tokens": 4000 + inputImage, "input_tokens_details": input, "output_tokens_details": map[string]int{"image_tokens": 3000}}
					return workerJSON(map[string]any{"data": []any{resultItem(1), map[string]any{"error": map[string]string{"code": "1027", "message": "private failure detail"}}, resultItem(3)}, "output_format": "png", "usage": usage}), nil
				})}
				a := minimax.NewH3Fixture(t, native)
				repo := postgres.NewTaskRepository(db)
				service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"minimax": a}, map[string]string{"minimax": model}, "minimax")
				service.SetAdmissionScopeResolver(func(context.Context, string) (core.ProviderConnectionScope, error) {
					return core.ProviderConnectionScope{ID: "minimax", OwnerID: identity.SystemOwnerID, Revision: revision}, nil
				})
				service.EnableAccounting()
				admitted, err := service.CreateMediaRequest(scope, core.CreateMediaInput{Model: model, Prompt: "fixture", Provider: "minimax", MediaType: "image", Options: options, ReferenceEvidence: options.ReferenceMediaEvidence}, "h3-image-fixture")
				if err != nil || admitted.Cost == nil || !admitted.Cost.QuantityUnknown {
					t.Fatal("fixture admission and tariff", err, admitted.Cost)
				}
				id := admitted.ID
				root := t.TempDir()
				local, err := storage.NewLocal(root, mediaClient, 1<<20)
				if err != nil {
					t.Fatal(err)
				}
				firstCtx, stop := context.WithCancel(scope)
				defer stop()
				before := 1
				if mode == "edit" {
					before = 2
				}
				firstStore := &h3InterruptedStore{Local: local, before: before, stop: stop}
				first := worker.New(repo, q, a, firstStore, time.Millisecond, 12*time.Minute, 1, time.Millisecond)
				first.OperationConcurrency = 1
				first.ArchiveConcurrency = 1
				first.ShutdownTimeout = 20 * time.Millisecond
				if err := first.Run(firstCtx); !errors.Is(err, worker.ErrShutdownTimeout) {
					t.Fatal("interruption", err)
				}
				var receipts int
				if err := db.QueryRowContext(ctx, "SELECT count(*) FROM accepted_provider_receipts WHERE task_id=$1", id).Scan(&receipts); err != nil || receipts != 1 {
					t.Fatal("receipt not durable", err, receipts)
				}
				pending, err := repo.Get(scope, id)
				if err != nil || pending.Status != core.TaskRunning {
					t.Fatal("pending state", err, pending.Status)
				}
				public, _ := json.Marshal(core.GenerationView(pending))
				for _, secret := range []string{"media.fixture", "signature=private", "data:image", "private enhanced wording", "private failure detail"} {
					if bytes.Contains(public, []byte(secret)) {
						t.Fatal("private output exposed")
					}
				}
				reopened, err := postgres.Open(ctx, dsn)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				fresh := postgres.NewTaskRepository(reopened)
				local, err = storage.NewLocal(root, mediaClient, 1<<20)
				if err != nil {
					t.Fatal(err)
				}
				next, finish := context.WithCancel(scope)
				second := worker.New(fresh, q, minimax.NewH3Fixture(t, native), local, time.Millisecond, 12*time.Minute, 1, time.Millisecond)
				second.ArchiveConcurrency = 1
				second.OperationConcurrency = 1
				done := make(chan error, 1)
				go func() { done <- second.Run(next) }()
				defer func() {
					finish()
					if err := <-done; err != nil {
						t.Error(err)
					}
				}()
				var result core.Task
				for {
					result, err = fresh.Get(scope, id)
					if err != nil {
						t.Fatal(err)
					}
					if result.Status == core.TaskSucceeded || result.Status == core.TaskFailed {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(5 * time.Millisecond):
					}
				}
				wantFetches := int32(3) // Interrupted read may repeat a GET. Completed checkpoints are reused.
				if inline {
					wantFetches = 0
				}
				if result.Status != core.TaskSucceeded || len(result.StoredArtifacts) != 2 || posts.Load() != 1 || fetches.Load() != wantFetches || result.Cost == nil || result.Cost.ActualUSD != nil {
					t.Fatal("unexpected recovery", result.Status, posts.Load(), fetches.Load(), result.Cost)
				}
				expectedCost := money.USD(13700)
				if mode == "edit" {
					expectedCost += 3000
				}
				if inline {
					expectedCost = 25100
					if mode == "edit" {
						expectedCost += 6000
					}
				}
				if missing {
					if result.Cost.MeteredUSD != nil {
						t.Fatal("fabricated metering")
					}
				} else if result.Cost.MeteredUSD == nil || *result.Cost.MeteredUSD != expectedCost {
					t.Fatal("missing or double-counted tokens", result.Cost)
				}
				for _, artifact := range result.StoredArtifacts {
					obj, err := local.Open(scope, artifact.StorageKey)
					if err != nil {
						t.Fatal(err)
					}
					b, err := io.ReadAll(obj.Reader)
					obj.Reader.Close()
					if err != nil || !bytes.Equal(b, png) {
						t.Fatal("archived bytes differ")
					}
				}
				final, _ := json.Marshal(core.GenerationView(result))
				if strings.Contains(string(final), "media.fixture") || strings.Contains(string(final), "private enhanced") || strings.Contains(string(final), "data:image/") || strings.Contains(string(final), base64.StdEncoding.EncodeToString(png)) {
					t.Fatal("private output exposed")
				}
				if err := reopened.QueryRowContext(ctx, "SELECT count(*) FROM accepted_provider_receipts WHERE task_id=$1", id).Scan(&receipts); err != nil || receipts != 0 {
					t.Fatal("receipt retention", err, receipts)
				}
				t.Logf("POSTs=%d archived=%d HTTP fetches=%d restart before artifact=%d missing usage category=%t", posts.Load(), len(result.StoredArtifacts), fetches.Load(), before, missing)
			})
		}
	}
}
