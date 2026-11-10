package worker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
	"github.com/google/uuid"
)

const qwen31WorkerEndpoint = "https://fixture-workspace.ap-southeast-1.maas.aliyuncs.com/api/v1"

type qwen31WorkerTransport func(*http.Request) (*http.Response, error)

func (f qwen31WorkerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func qwen31WorkerWAV() []byte {
	b := make([]byte, 46)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 38)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 24000)
	binary.LittleEndian.PutUint32(b[28:], 48000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 2)
	return b
}

func qwen31WorkerMP3() []byte {
	b := make([]byte, 417) // One complete MPEG-1 Layer III frame, 128 kbps/44.1 kHz.
	copy(b, []byte{0xff, 0xfb, 0x90, 0x00})
	return b
}

func qwen31WorkerResponse(r *http.Request, usage map[string]any) *http.Response {
	body, _ := json.Marshal(map[string]any{
		"request_id": "private-request", "usage": usage,
		"output": map[string]any{"finish_reason": "stop", "audio": map[string]any{
			"id": "private-audio", "url": "https://qwen-media.example.test/result.wav?signature=fixture",
			"expires_at": time.Now().Add(time.Hour).Unix(), "data": "", "duration": 1,
		}},
	})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body)), Request: r}
}

func TestQwen31TypedDownloadRejectsContentAndLengthMismatch(t *testing.T) {
	for _, tc := range []struct {
		name, mime string
		body       []byte
		size       int64
		ok         bool
		format     string
	}{
		{"wav", "audio/wav", qwen31WorkerWAV(), 46, true, "wav"},
		{"mp3", "audio/mpeg", qwen31WorkerMP3(), 417, true, "mp3"},
		{"html-mime", "text/html", []byte("<html>private</html>"), 20, false, "wav"},
		{"html-spoof", "audio/wav", []byte("<html>private</html>"), 20, false, "wav"},
		{"unknown-binary-wav", "audio/wav", bytes.Repeat([]byte{0}, 64), 64, false, "wav"},
		{"unknown-binary-mp3", "audio/mpeg", bytes.Repeat([]byte{0}, 64), 64, false, "mp3"},
		{"id3-without-frame", "audio/mpeg", []byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 0}, 10, false, "mp3"},
		{"truncated", "audio/wav", qwen31WorkerWAV(), 100, false, "wav"},
		{"truncated-mp3-frame", "audio/mpeg", qwen31WorkerMP3()[:20], 20, false, "mp3"},
		{"empty", "audio/wav", nil, 0, false, "wav"},
		{"oversized", "audio/wav", nil, (128 << 20) + 1, false, "wav"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			posts, downloads := 0, 0
			client := &http.Client{Transport: qwen31WorkerTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPost && r.URL.String() == qwen31WorkerEndpoint+"/services/audio/tts/SpeechSynthesizer" {
					posts++
					return qwen31WorkerResponse(r, map[string]any{"duration": 1}), nil
				}
				if r.Method != http.MethodGet || r.URL.Host != "qwen-media.example.test" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Fatal("wrong target or leaked download credentials")
				}
				downloads++
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.mime}}, ContentLength: tc.size, Body: io.NopCloser(bytes.NewReader(tc.body)), Request: r}, nil
			})}
			p, err := alibaba.New("synthetic-key", qwen31WorkerEndpoint, "", "", client)
			if err != nil {
				t.Fatal(err)
			}
			ctx := core.WithProviderRevision(identity.WithOwnerID(context.Background(), "fixture-owner"), "revision")
			accepted, err := p.Generate(ctx, core.MediaRequest{Model: "qwen-audio-3.1-tts-next", Prompt: "Synthetic rain.", Options: core.MediaOptions{Format: tc.format}})
			if err != nil || posts != 1 || downloads != 0 || len(accepted.Artifacts) != 1 || accepted.Artifacts[0].SourceReference == "" {
				t.Fatal("native submission downloaded eagerly", posts, downloads, err)
			}
			source, err := p.OpenArtifact(ctx, accepted.Artifacts[0].SourceReference)
			if err == nil {
				store, e := storage.NewLocal(t.TempDir(), client, 1<<20)
				if e != nil {
					t.Fatal(e)
				}
				_, err = store.ArchiveSource(ctx, "fixture", source)
			}
			if (err == nil) != tc.ok || posts != 1 || downloads != 1 {
				t.Fatal("typed download validation or replay failure", posts, downloads, err)
			}
		})
	}
}

// Runs only under the repository's disposable PostgreSQL/Redis harness.
// Reopen repository, adapter and storage after durable receipt persistence.
func TestQwen31AcceptedAudioRestartDoesNotResubmitOrDoubleCharge(t *testing.T) {
	for _, tc := range []struct {
		id      string
		usage   map[string]any
		pricing core.PricingSnapshot
		metered *money.USD
		unknown bool
	}{
		{"qwen-audio-3.1-tts-flash", map[string]any{"input_tokens": 12, "output_tokens": 30}, core.PricingSnapshot{Currency: "USD", Unit: "token", Per: 1_000_000, Source: "fixture public tariff", Components: []core.PricingComponent{{Metric: "input_tokens", Price: 230000, Per: 1_000_000}, {Metric: "output_tokens", Price: 1870000, Per: 1_000_000}}}, qwen31WorkerMoney(60), true},
		{"qwen-audio-3.1-tts-plus", map[string]any{"characters": 4, "input_tokens": 12, "output_tokens": 30}, core.PricingSnapshot{Currency: "USD", Unit: "character", Per: 10000, Price: 300000}, qwen31WorkerMoney(120), false},
		{"qwen-audio-3.1-tts-next", map[string]any{"duration": 120}, core.PricingSnapshot{Currency: "USD", Unit: "token", Per: 1_000_000, Source: "fixture public tariff", Components: []core.PricingComponent{{Metric: "input_tokens", Price: 930000, Per: 1_000_000}, {Metric: "output_tokens", Price: 1870000, Per: 1_000_000}}}, nil, true},
	} {
		t.Run(tc.id, func(t *testing.T) {
			base, db, repo, _, _, _ := operationTestServices(t)
			ctx := identity.WithOwnerID(base, identity.SystemOwnerID)
			now, id := time.Now().UTC(), uuid.NewString()
			options := core.MediaOptions{Format: "wav"}
			if tc.id != "qwen-audio-3.1-tts-next" {
				options.Voice = "longanhuan_v3.1"
			}
			task := core.Task{ID: id, Status: core.TaskQueued, Provider: "alibaba", Model: tc.id, MediaType: "speech", Prompt: "你好", Options: options, ProviderIdentity: "fixture-revision", CreatedAt: now, UpdatedAt: now,
				Cost: &core.TaskCost{QuantityUnknown: tc.unknown, Status: "estimated", Pricing: tc.pricing}}
			if _, _, err := repo.CreateOrGet(ctx, task, "", "qwen31-receipt-fixture"); err != nil {
				t.Fatal(err)
			}
			task, err := repo.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			ctx = identity.WithResourceOwners(ctx, task.ProviderOwnerID, task.StorageOwnerID)
			var posts, downloads atomic.Int32
			newClient := func() *http.Client {
				return &http.Client{Transport: qwen31WorkerTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method == http.MethodPost && r.URL.String() == qwen31WorkerEndpoint+"/services/audio/tts/SpeechSynthesizer" {
						posts.Add(1)
						return qwen31WorkerResponse(r, tc.usage), nil
					}
					if r.Method != http.MethodGet || r.URL.Host != "qwen-media.example.test" || r.Header.Get("Authorization") != "" {
						t.Fatal("receipt polled, resubmitted or leaked credentials")
					}
					downloads.Add(1)
					data := qwen31WorkerWAV()
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"audio/wav"}}, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
				})}
			}
			firstCtx, cancel := context.WithCancel(core.WithClaimToken(ctx, uuid.NewString()))
			if ok, err := repo.AcquireTask(firstCtx, id, time.Minute); err != nil || !ok {
				t.Fatal(ok, err)
			}
			client := newClient()
			provider, err := alibaba.New("synthetic-key", qwen31WorkerEndpoint, "", "", client)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			local, err := storage.NewLocal(root, client, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			first := New(repo, nil, provider, &qwen31InterruptSourceStore{Local: local, cancel: cancel}, time.Millisecond, time.Minute, 2, time.Second)
			if err := first.process(firstCtx, id); !errors.Is(err, context.Canceled) {
				t.Fatal("expected interruption after durable receipt", err)
			}
			cancel()
			if err := repo.ReleaseTask(context.WithoutCancel(firstCtx), id); err != nil {
				t.Fatal(err)
			}
			var receiptCount int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM accepted_provider_receipts WHERE task_id=$1`, id).Scan(&receiptCount); err != nil || receiptCount != 1 || posts.Load() != 1 {
				t.Fatal("native result was not durably retained", receiptCount, posts.Load(), err)
			}
			var searchPath string
			if err := db.QueryRowContext(ctx, `SHOW search_path`).Scan(&searchPath); err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(os.Getenv("GATEWAY_TEST_DATABASE_URL"))
			if err != nil {
				t.Fatal(err)
			}
			query := parsed.Query()
			query.Set("search_path", searchPath)
			parsed.RawQuery = query.Encode()
			reopened, err := postgres.Open(ctx, parsed.String())
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			recoveredRepo := postgres.NewTaskRepository(reopened)
			recoveredClient := newClient()
			recoveredProvider, err := alibaba.New("synthetic-key", qwen31WorkerEndpoint, "", "", recoveredClient)
			if err != nil {
				t.Fatal(err)
			}
			recoveredStore, err := storage.NewLocal(root, recoveredClient, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			secondCtx := core.WithClaimToken(ctx, uuid.NewString())
			if ok, err := recoveredRepo.AcquireTask(secondCtx, id, time.Minute); err != nil || !ok {
				t.Fatal(ok, err)
			}
			second := New(recoveredRepo, nil, recoveredProvider, recoveredStore, time.Millisecond, time.Minute, 2, time.Second)
			if err := second.process(secondCtx, id); err != nil {
				t.Fatal(err)
			}
			done, err := recoveredRepo.Get(ctx, id)
			if err != nil || done.Status != core.TaskSucceeded || posts.Load() != 1 || downloads.Load() != 2 || len(done.StoredArtifacts) != 1 || done.StoredArtifacts[0].ID != "1" {
				t.Fatal("receipt recovery lost output or resubmitted", done.Status, posts.Load(), err)
			}
			if done.Cost == nil || tc.metered == nil && done.Cost.MeteredUSD != nil || tc.metered != nil && (done.Cost.MeteredUSD == nil || *done.Cost.MeteredUSD != *tc.metered) {
				t.Fatal("receipt changed billing dimension or double-counted", done.Cost)
			}
			public, _ := json.Marshal(core.GenerationView(done))
			for _, secret := range []string{"signature=", "qwen-media.example.test", "alibaba-qwen31:", "synthetic-key"} {
				if bytes.Contains(public, []byte(secret)) {
					t.Fatal("private accepted source leaked", secret)
				}
			}
			if err := second.process(secondCtx, id); err != nil || posts.Load() != 1 {
				t.Fatal("terminal delivery submitted again", posts.Load(), err)
			}
		})
	}
}

type qwen31InterruptSourceStore struct {
	*storage.Local
	cancel context.CancelFunc
}

func (s *qwen31InterruptSourceStore) ArchiveSource(ctx context.Context, _ string, source core.ArtifactSource) (core.StoredMedia, error) {
	defer source.Reader.Close()
	s.cancel()
	return core.StoredMedia{}, ctx.Err()
}

func qwen31WorkerMoney(v money.USD) *money.USD { return &v }
