package minimax

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
)

func TestH3ImageTransportBudgetAndRedirect(t *testing.T) {
	const path = "/v1/images/generations"
	for _, tc := range []struct {
		name           string
		conn, override time.Duration
		status         int
		wantWait       time.Duration
		wantErr        error
	}{
		{"default", 0, 0, 200, 630 * time.Second, nil},
		{"short connection", 30 * time.Second, 0, 200, 0, httpclient.ErrSinglePOSTConfiguration},
		{"short override", 0, time.Minute, 200, 0, httpclient.ErrSinglePOSTConfiguration},
		{"explicit minimum", 30 * time.Second, 600 * time.Second, 200, 600 * time.Second, nil},
		{"long override bounded", 0, 24 * time.Hour, 200, 630 * time.Second, nil},
		{"worker deadline", 0, 0, 200, 0, httpclient.ErrSinglePOSTConfiguration},
		{"redirect", 0, 0, 307, 630 * time.Second, core.ErrProviderSubmissionUncertain},
		{"provider timeout", 0, 0, 504, 630 * time.Second, core.ErrProviderSubmissionUncertain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			a := fixture(t, func(r *http.Request) (*http.Response, error) {
				calls++
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > tc.wantWait || time.Until(deadline) < tc.wantWait-time.Second || r.GetBody != nil || r.URL.Path != path || r.Method != "POST" {
					t.Fatal("unexpected final request or deadline")
				}
				resp := response(tc.status, map[string]any{"data": []any{map[string]string{"url": "https://result.invalid/fixture"}}})
				resp.Header.Set("Location", DefaultBaseURL+"/unexpected")
				return resp, nil
			})
			a.connectionTimeout = tc.conn
			ctx := context.Background()
			if tc.name == "worker deadline" {
				ctx = httpclient.WithSinglePOSTDeadline(ctx, time.Now().Add(time.Minute))
			}
			if tc.override > 0 {
				ctx = httpclient.WithRequestTimeout(ctx, tc.override)
			}
			_, err := a.do(ctx, "POST", path, strings.NewReader("{}"), "application/json")
			if !errors.Is(err, tc.wantErr) || (tc.wantWait == 0 && calls != 0) || (tc.wantWait > 0 && calls != 1) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
	disabled := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("disabled model dispatched"); return nil, nil })
	for _, id := range []string{"MiniMax-H3-Image", "MiniMax-H3-Image-Highspeed"} {
		if _, found := modelFor(id); found {
			t.Fatalf("unfinished model advertised: %s", id)
		}
		if _, err := disabled.Generate(owned("fixture"), core.MediaRequest{Model: id, Prompt: "fixture"}); err == nil {
			t.Fatalf("unfinished model accepted: %s", id)
		}
	}
}
