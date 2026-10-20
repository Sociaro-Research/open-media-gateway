package bytedance

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

const pollSuccess = `{"status":"succeeded","content":{"video_url":"https://media.example.test/result.mp4"},"usage":{"total_tokens":42}}`

func TestPollResponseIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType string
		code                    int
		want                    core.ProviderStatus
	}{
		{"success", pollSuccess, "application/json", 200, core.ProviderSucceeded},
		{"whitespace", " \n" + pollSuccess + "\r\n\t", "application/json; charset=utf-8", 200, core.ProviderSucceeded},
		// Preserve the existing body-based contract; a mislabeled valid JSON body is accepted.
		{"valid JSON with text type", pollSuccess, "text/plain", 200, core.ProviderSucceeded},
		{"queued", `{"status":"queued"}`, "application/json", 200, core.ProviderQueued},
		{"running", `{"status":"running"}`, "application/json", 200, core.ProviderRunning},
		{"failed", `{"status":"failed","error":{"message":"synthetic failure"}}`, "application/json", 200, core.ProviderFailed},
		{"cancelled", `{"status":"cancelled"}`, "application/json", 200, core.ProviderFailed},
		{"expired", `{"status":"expired"}`, "application/json", 200, core.ProviderFailed},
		{"malformed", `{"status":!}`, "application/json", 200, ""},
		{"truncated", strings.TrimSuffix(pollSuccess, "}"), "application/json", 200, ""},
		{"trailing garbage", pollSuccess + "<html>proxy failure</html>", "application/json", 200, ""},
		{"trailing truncated JSON", pollSuccess + `{"error":`, "application/json", 200, ""},
		{"second JSON document", pollSuccess + `{"status":"failed"}`, "application/json", 200, ""},
		{"empty success", "", "application/json", 200, ""},
		{"no content", "", "", 204, ""},
		{"HTML success", "<html>proxy failure</html>", "text/html", 200, ""},
		{"JSON error success", `{"error":{"message":"synthetic failure"}}`, "application/json", 200, ""},
		{"HTTP error JSON", pollSuccess, "application/json", 503, ""},
		{"HTTP error HTML", "<html>proxy failure</html>", "text/html", 502, ""},
		{"HTTP error empty", "", "", 429, ""},
		{"null", "null", "application/json", 200, ""},
		{"empty object", "{}", "application/json", 200, ""},
		{"wrong shape", `{"status":42}`, "application/json", 200, ""},
		{"missing result", `{"status":"succeeded"}`, "application/json", 200, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &observedResponseBody{Reader: strings.NewReader(tc.body)}
			calls := 0
			adapter, err := New("synthetic-key", "https://provider.example.test", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != tasksPath+"/accepted-task" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				return &http.Response{StatusCode: tc.code, Header: http.Header{"Content-Type": {tc.contentType}}, Body: body}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.GetTask(context.Background(), "accepted-task")
			if calls != 1 || !body.closed {
				t.Fatalf("requests=%d, body closed=%v", calls, body.closed)
			}
			if tc.want == "" {
				if err == nil || !reflect.DeepEqual(result, core.ProviderResult{}) {
					t.Fatalf("invalid response accepted: result=%+v error=%v", result, err)
				}
				return
			}
			if err != nil || result.Status != tc.want {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if tc.want == core.ProviderSucceeded && (result.ResultURL != "https://media.example.test/result.mp4" || result.Usage == nil || len(result.Usage.Metrics) != 1 || result.Usage.Metrics[0].Quantity != 42) {
				t.Fatalf("success lost result or usage: %+v", result)
			}
		})
	}
}

func TestPollResponseSizeBound(t *testing.T) {
	const limit = 1 << 20 // Existing adapter response budget, including trailing whitespace.
	for _, tc := range []struct {
		name   string
		size   int
		code   int
		wantOK bool
	}{
		{"exact limit", limit, 200, true},
		{"one byte over", limit + 1, 200, false},
		{"large success", 4 * limit, 200, false},
		{"large error", 4 * limit, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &observedResponseBody{Reader: strings.NewReader(pollSuccess + strings.Repeat(" ", tc.size-len(pollSuccess)))}
			adapter := &Adapter{baseURL: "https://provider.example.test", client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.code, Body: body}, nil
			})}}
			result, err := adapter.GetTask(context.Background(), "accepted-task")
			if body.read > limit+1 || !body.closed {
				t.Fatalf("read=%d closed=%v", body.read, body.closed)
			}
			if tc.wantOK {
				if err != nil || result.Status != core.ProviderSucceeded {
					t.Fatalf("exact budget response rejected: result=%+v error=%v", result, err)
				}
			} else if err == nil || !reflect.DeepEqual(result, core.ProviderResult{}) {
				t.Fatalf("invalid response accepted after reading %d bytes: result=%+v error=%v", body.read, result, err)
			}
			t.Logf("read=%d closed=%v success=%v", body.read, body.closed, err == nil)
		})
	}
}

func TestPollRejectsTruncatedHTTPBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The JSON is complete, but the HTTP body is truncated in transit.
		w.Header().Set("Content-Length", "1000")
		_, _ = io.WriteString(w, pollSuccess)
	}))
	defer server.Close()
	adapter, err := New("synthetic-key", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.GetTask(context.Background(), "accepted-task")
	if !errors.Is(err, io.ErrUnexpectedEOF) || !reflect.DeepEqual(result, core.ProviderResult{}) {
		t.Fatalf("truncated HTTP body accepted: result=%+v error=%v", result, err)
	}
}

func TestPollContextCancellation(t *testing.T) {
	for _, afterPrefix := range []bool{false, true} {
		t.Run(map[bool]string{false: "before headers", true: "after complete JSON prefix"}[afterPrefix], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reading := make(chan struct{})
			var body *observedResponseBody
			adapter := &Adapter{baseURL: "https://provider.example.test", client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if !afterPrefix {
					close(reading)
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				body = &observedResponseBody{Reader: io.MultiReader(strings.NewReader(pollSuccess), contextResponseReader{ctx: r.Context(), reading: reading})}
				return &http.Response{StatusCode: 200, Body: body}, nil
			})}}
			type outcome struct {
				result core.ProviderResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := adapter.GetTask(ctx, "accepted-task")
				done <- outcome{result, err}
			}()
			select {
			case <-reading:
				cancel()
			case got := <-done:
				t.Fatalf("returned before response EOF/cancellation: %+v", got)
			case <-time.After(5 * time.Second):
				t.Fatal("did not reach blocked response read")
			}
			select {
			case got := <-done:
				if !errors.Is(got.err, context.Canceled) || !reflect.DeepEqual(got.result, core.ProviderResult{}) {
					t.Fatalf("cancellation lost: %+v", got)
				}
				if afterPrefix && !body.closed {
					t.Fatal("cancelled response body not closed")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("poll did not stop after cancellation")
			}
		})
	}
}

type observedResponseBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *observedResponseBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}

func (b *observedResponseBody) Close() error { b.closed = true; return nil }

type contextResponseReader struct {
	ctx     context.Context
	reading chan struct{}
}

func (r contextResponseReader) Read([]byte) (int, error) {
	close(r.reading)
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}
