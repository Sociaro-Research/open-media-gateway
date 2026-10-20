package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/httpclient"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
)

func TestAllAdapterTimeoutPrecedence(t *testing.T) {
	for _, tc := range []struct{ provider, model, body string }{
		{"bytedance", "seedance-model", `{"id":"job"}`},
		{"bytedance", "seedream-4-5-251128", `{"data":[{"url":"https://cdn.example/image.png"}]}`},
		{"openai", "gpt-image-2.5-flare", `{"data":[{"b64_json":"aW1hZ2U="}]}`},
		{"agnes", "agnes-image-2.0-flash", `{"data":[{"url":"https://cdn.example/image.png"}]}`},
		{"alibaba", "wan2.6-t2v", `{"output":{"task_id":"job"}}`},
		{"sonilo", "text-to-music", `{"task_id":"job","status":"processing"}`},
	} {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			m, _, ctx := fixtureConnection(t)
			m.client.Timeout = 30 * time.Second
			want := 30 * time.Second
			calls := 0
			m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Error("no HTTP deadline")
				}
				left := time.Until(deadline)
				if left > want || left < want-time.Second {
					t.Errorf("deadline=%v want %v", left, want)
				}
				raw, _ := io.ReadAll(r.Body)
				if strings.Contains(string(raw), "timeout_seconds") {
					t.Error("gateway timeout forwarded to vendor")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			credentials := Credentials{APIKey: "fixture-key", BaseURL: "https://api.example/v1"}
			if tc.provider == "openai" {
				credentials.BaseURL = "https://api.openai.com/v1"
			}
			if tc.provider == "agnes" {
				credentials.BaseURL = "https://apihub.agnes-ai.com/v1"
			}
			if tc.provider == "sonilo" {
				credentials.BaseURL = "https://api.sonilo.com/v1"
			}
			request := core.MediaRequest{Model: tc.model, Prompt: "scene", Options: core.MediaOptions{}}
			if tc.provider == "sonilo" {
				request.Options.Duration = 5
			}
			adapter, err := m.buildAdapter(tc.provider, credentials)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = adapter.Generate(ctx, request); err != nil {
				t.Fatal(err)
			}
			credentials.Connection = &providerkit.Connection{TimeoutSeconds: pointer(int64(180))}
			adapter, err = m.buildAdapter(tc.provider, credentials)
			if err != nil {
				t.Fatal(err)
			}
			want = 180 * time.Second
			if _, err = adapter.Generate(ctx, request); err != nil {
				t.Fatal(err)
			}
			want = 600 * time.Second
			if _, err = adapter.Generate(httpclient.WithRequestTimeout(ctx, want), request); err != nil {
				t.Fatal(err)
			}
			if m.client.Timeout != 30*time.Second || calls != 3 {
				t.Fatal("shared timeout changed or request retried", calls)
			}
		})
	}
}

func TestManagedRequestTimeoutOverridesPinnedConnection(t *testing.T) {
	m, repo, ctx := fixtureConnection(t)
	m.client.Timeout = 30 * time.Second
	connection, err := m.Create(ctx, CreateConnection{APIType: "bytedance", Name: "Timeout"})
	if err != nil {
		t.Fatal(err)
	}
	model := manualVideo(m)
	cfg := &providerkit.Connection{ManualCatalog: true, Models: []providerkit.ManualModel{model}, TimeoutSeconds: pointer(int64(180))}
	if _, err = m.Update(ctx, connection.ID, ProviderUpdate{APIKey: pointer("key"), Connection: cfg, Enabled: pointer(true)}); err != nil {
		t.Fatal(err)
	}
	p, err := m.Provider(ctx, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := p.(core.CostQuoter).Quote(ctx, model.Model, core.MediaRequest{Model: model.Model.ID})
	if err != nil {
		t.Fatal(err)
	}
	cfg.TimeoutSeconds = pointer(int64(20))
	if _, err = m.Update(ctx, connection.ID, ProviderUpdate{Connection: cfg}); err != nil {
		t.Fatal(err)
	}
	restarted := NewManager(repo, m.cipher, m.cfg)
	restarted.client = m.client
	want := 180 * time.Second
	restarted.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		d, _ := r.Context().Deadline()
		if left := time.Until(d); left > want || left < want-time.Second {
			t.Errorf("deadline=%v want %v", left, want)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"job"}`))}, nil
	})
	p, err = restarted.Provider(ctx, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := core.MediaRequest{Model: model.Model.ID, ProviderIdentity: revision, Prompt: "scene"}
	if _, err = p.Generate(ctx, request); err != nil {
		t.Fatal(err)
	}
	request.Options.TimeoutSeconds = pointer(int64(600))
	want = 600 * time.Second
	if _, err = p.Generate(ctx, request); err != nil {
		t.Fatal(err)
	}
	request.Options.TimeoutSeconds = pointer(int64(5))
	want = 5 * time.Second
	if _, err = p.Generate(ctx, request); err != nil {
		t.Fatal(err)
	}
	request.ProviderIdentity = ""
	request.Options.TimeoutSeconds = nil
	want = 20 * time.Second
	if _, err = p.Generate(ctx, request); err != nil {
		t.Fatal(err)
	}
	cfg.TimeoutSeconds = nil
	if _, err = m.Update(ctx, connection.ID, ProviderUpdate{Connection: cfg}); err != nil {
		t.Fatal(err)
	}
	want = 30 * time.Second
	if _, err = p.Generate(ctx, request); err != nil {
		t.Fatal(err)
	}
	cfg.TimeoutSeconds = pointer(int64(20))
	if _, err = m.Update(ctx, connection.ID, ProviderUpdate{Connection: cfg}); err != nil {
		t.Fatal(err)
	}
	bad := int64(0)
	cfg.TimeoutSeconds = &bad
	if _, err = m.Update(ctx, connection.ID, ProviderUpdate{Connection: cfg}); err == nil {
		t.Fatal("invalid saved timeout accepted")
	}
	// timeout is part of the encrypted revision while the public settings are readable.
	output, err := restarted.Get(context.WithoutCancel(ctx), connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(output)
	if !strings.Contains(string(raw), `"timeout_seconds":20`) {
		t.Fatal(string(raw))
	}
}
