package providers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
)

func TestAlibabaManagedQwenAndPixverseKeepIndependentRecoveryContracts(t *testing.T) {
	m, _, ctx := fixtureConnection(t)
	endpoint, key := "https://fixture.ap-southeast-1.maas.aliyuncs.com/api/v1", "synthetic-alibaba-key"
	c := providerkit.Credentials{APIKey: key, BaseURL: endpoint}
	m.cfg.Providers = map[string]providerkit.Settings{"alibaba": {"pixverse_reviewed_connection": alibaba.Definition().Identity(c)}}
	posts, polls, downloads := 0, 0, 0
	currentKey := key
	dropAdapter := func(id string) {
		m.mu.Lock()
		delete(m.adapters, m.cacheKey(ctx, id))
		m.mu.Unlock()
	}
	m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"output":{"total":0,"models":[]}}`
		if r.URL.Host == "audio.example.test" {
			downloads++
			if r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Synthetic-Private") != "" || r.Header.Get("Cookie") != "" {
				t.Fatal("private Qwen download leaked credentials")
			}
			wav := testmedia.WAV()
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"audio/wav"}}, ContentLength: int64(len(wav)), Body: io.NopCloser(strings.NewReader(string(wav)))}, nil
		}
		wantKey := key
		if strings.HasSuffix(r.URL.Path, "/models") {
			wantKey = currentKey
		}
		if r.Header.Get("Authorization") != "Bearer "+wantKey || r.URL.Host != "fixture.ap-southeast-1.maas.aliyuncs.com" {
			t.Fatal("lost pinned Alibaba credentials", r.Method, r.URL.Host, r.URL.Path)
		}
		switch {
		case r.Method == "POST":
			posts++
			if strings.HasSuffix(r.URL.Path, "SpeechSynthesizer") {
				body = `{"request_id":"qwen-request","output":{"finish_reason":"stop","audio":{"id":"qwen-native","url":"https://audio.example.test/result.wav","expires_at":` + jsonNumber(time.Now().Add(time.Hour).Unix()) + `}},"usage":{"duration":1}}`
			} else {
				body = `{"output":{"task_id":"pixverse-native","task_status":"PENDING"}}`
			}
		case strings.Contains(r.URL.Path, "/tasks/"):
			polls++
			if !strings.HasSuffix(r.URL.Path, "/pixverse-native") {
				t.Fatal("native task ID rewritten")
			}
			body = `{"output":{"task_id":"pixverse-native","task_status":"SUCCEEDED","video_url":"https://video.example.test/result.mp4","image_url":"https://video.example.test/wrong.png"},"usage":{"duration":3,"video_count":1,"fps":24,"SR":"720","size":"1280*720","audio":false,"video_duration":999}}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	connection, err := m.Create(ctx, CreateConnection{APIType: "alibaba", Name: "Offline native contracts"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Update(ctx, connection.ID, ProviderUpdate{APIKey: &key, BaseURL: &endpoint, Enabled: pointer(true), Connection: &providerkit.Connection{Headers: []providerkit.Header{{Name: "X-Synthetic-Private", Value: pointer("api-only")}}}}); err != nil {
		t.Fatal(err)
	}
	p, err := m.Provider(ctx, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	qwen := core.MediaRequest{Model: "qwen-audio-3.1-tts-next", Prompt: "Synthetic rain", Options: core.MediaOptions{Format: "wav"}}
	quote, revision, err := p.(core.CostQuoter).Quote(ctx, core.MediaModel{ID: qwen.Model}, qwen)
	if err != nil || revision == "" || !quote.QuantityUnknown || len(quote.Pricing.Components) != 2 {
		t.Fatal("PixVerse replaced Qwen quotation", quote, revision, err)
	}
	accepted, err := p.Generate(core.WithProviderRevision(ctx, revision), qwen)
	if err != nil || accepted.ID != "qwen-native" || len(accepted.Artifacts) != 1 || accepted.Artifacts[0].SourceReference == "" {
		t.Fatal("PixVerse replaced Qwen immediate receipt", accepted, err)
	}
	for _, mode := range []string{"t2v", "it2v", "kf2v", "r2v"} {
		r := core.MediaRequest{Model: "pixverse/pixverse-v6-" + mode, Prompt: "Synthetic", Options: core.MediaOptions{Duration: 5, Resolution: "720p"}}
		switch mode {
		case "t2v":
			r.Options.Ratio = "16:9"
		case "it2v":
			r.Options.ImageURLs = []string{"https://media.example.test/first.png"}
		case "kf2v":
			r.Options.ImageURLs = []string{"https://media.example.test/first.png", "https://media.example.test/last.png"}
		case "r2v":
			r.Options.Ratio = "16:9"
			r.Options.ImageURLs = []string{"https://media.example.test/person.png", "https://media.example.test/guitar.png"}
			r.Prompt = "@person holds @guitar "
			r.Options.Parameters = map[string]any{"reference_names": []string{"person", "guitar"}}
		}
		cost, rev, err := p.(core.CostQuoter).Quote(ctx, core.MediaModel{ID: r.Model, MediaType: "video"}, r)
		if err != nil || cost.ExpectedUSD != 200000 || rev != revision {
			t.Fatal("independent PixVerse quote lost", cost, rev, err)
		}
		job, err := p.Generate(core.WithProviderRevision(ctx, rev), r)
		if err != nil || job.ID != "pixverse-native" {
			t.Fatal(job, err)
		}
		// Reconstruct the pinned adapter and withdraw admission attestation.
		// Existing tasks still poll and Qwen accepted files still open.
		m.cfg.Providers["alibaba"] = providerkit.Settings{}
		dropAdapter(connection.ID)
		if _, _, err := p.(core.CostQuoter).Quote(ctx, core.MediaModel{ID: r.Model, MediaType: "video"}, r); !errors.Is(err, core.ErrProviderUnavailable) {
			t.Fatal("withdrawn review still admits a new generation", err)
		}
		pollCtx := core.WithProviderPollRequest(core.WithProviderRevision(ctx, rev), r)
		result, err := p.GetTask(pollCtx, job.ID)
		if err != nil || result.Status != core.ProviderSucceeded || result.ResultURL != "https://video.example.test/result.mp4" || result.Usage == nil || len(result.Usage.Metrics) != 1 || result.Usage.Metrics[0].Name != "duration" || result.Usage.Metrics[0].Quantity != 3 {
			t.Fatal("managed polling lost exact native context", result, err)
		}
		m.cfg.Providers["alibaba"]["pixverse_reviewed_connection"] = alibaba.Definition().Identity(c)
		dropAdapter(connection.ID)
	}
	// Rotate the current key; the old accepted Qwen receipt uses its own revision.
	currentKey = "rotated-synthetic-key"
	if _, err = m.Update(ctx, connection.ID, ProviderUpdate{APIKey: &currentKey}); err != nil {
		t.Fatal(err)
	}
	source, err := p.(core.ArtifactOpener).OpenArtifact(core.WithProviderRevision(ctx, revision), accepted.Artifacts[0].SourceReference)
	if err != nil {
		t.Fatal("Qwen source did not survive PixVerse integration and key rotation", err)
	}
	data, err := io.ReadAll(source.Reader)
	_ = source.Reader.Close()
	if err != nil || string(data) != string(testmedia.WAV()) || posts != 5 || polls != 4 || downloads != 1 {
		t.Fatal("unexpected native replay or corrupted result", posts, polls, downloads, err)
	}
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
