package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
)

type unavailableConnectionRepository struct{ remoteFixture }

func (*unavailableConnectionRepository) RemoteWorkspace(context.Context, string) (remote.Record, error) {
	return remote.Record{}, remote.ErrUnavailable
}

func TestConnectionDestinationFailsClosedBeforeOutboundRequest(t *testing.T) {
	for _, tc := range []struct {
		name, origin, body string
		repo               remote.Repository
		status             int
	}{
		{"unavailable saved connection", "https://local.fixture.test", `{"consent":true,"version":0}`, &unavailableConnectionRepository{}, 502},
		{"crafted host", "https://local.fixture.test", `{"consent":true,"version":0,"origin":"https://attacker.invalid"}`, &remoteFixture{}, 400},
		{"near match", "https://local.fixture.test", `{"consent":true,"version":0,"origin":"https://console.sociaro.ai:444"}`, &remoteFixture{}, 400},
		{"central cannot connect outward", installation.DefaultCentralOrigin, `{"consent":true,"version":0}`, &remoteFixture{}, 403},
		{"no consent", "https://local.fixture.test", `{"version":0}`, &remoteFixture{}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &Server{remoteWorkspaceManager: remote.New(tc.repo, nil, &http.Client{})}
			handler, e := WithPublicAPI(http.HandlerFunc(server.remoteAuthorization), "", tc.origin, "")
			if e != nil {
				t.Fatal(e)
			}
			r := httptest.NewRequest("POST", "https://local.fixture.test/v1/system/remote-workspace/authorization", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Forwarded", "host=console.sociaro.ai")
			r.Header.Set("X-Forwarded-Host", "console.sociaro.ai")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d want %d: %s", w.Code, tc.status, w.Body)
			}
		})
	}
}
