package providers

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

// A green catalogue fetch does not prove the new operational fields exist.
// Verify their delivery through the configured connection/capture path too.
func TestTemplateOperationalContractsSurviveCatalogueCapture(t *testing.T) {
	for _, tc := range []struct{ provider, id, types string }{
		{"bytedance", "dreamina-seedance-2-5-260628", "image,video,audio"},
		{"alibaba", "wan3.0-video", "image,video,audio"},
		{"alibaba", "wan2.7-r2v", "image,video"},
		{"alibaba", "happyhorse-1.1-r2v", "image"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			m, _, ctx := fixtureConnection(t)
			ctx = identity.WithResourceOwners(identity.WithOwnerID(ctx, identity.SharedOwnerID), identity.SharedOwnerID, identity.SharedOwnerID)
			m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/models") {
					t.Fatalf("unexpected catalogue request: %s %s", r.Method, r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"` + tc.id + `"}]}`)), Request: r}, nil
			})
			if _, err := m.Update(ctx, tc.provider, ProviderUpdate{APIKey: pointer("synthetic"), Enabled: pointer(true), Connection: &providerkit.Connection{}}); err != nil {
				t.Fatal(err)
			}
			capture, err := m.CaptureCatalog(ctx, tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, model := range capture.Models {
				if model.ID == tc.id {
					found = true
					if model.ReferenceMediaLimits == nil || strings.Join(model.ReferencePromptTypes, ",") != tc.types {
						t.Fatal("operational metadata omitted from successful capture", model)
					}
				}
			}
			if !found {
				t.Fatal("supported model missing from capture")
			}
		})
	}
}
