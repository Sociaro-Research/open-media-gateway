package catalogsync

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

type sourceAliasProvider struct{}

func (sourceAliasProvider) CaptureCatalog(context.Context, string) (Capture, error) {
	return Capture{}, nil
}
func (sourceAliasProvider) CheckCatalogRate(context.Context, string, core.MediaModel, Rate) error {
	return nil
}
func (sourceAliasProvider) ResolveCatalogSource(_ context.Context, _ string, s Source) Source {
	s.URL += ".md"
	return s
}

type sourceAliasTransport func(*http.Request) (*http.Response, error)

func (f sourceAliasTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRunnerRecordsActualResolvedPublicSourceURL(t *testing.T) {
	repo := &inactiveRunnerRepository{}
	client := &http.Client{Transport: sourceAliasTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://example.test/pricing.md" || r.Header.Get("Authorization") != "" {
			t.Fatal("incorrect public alias fetch", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("Public source evidence ", 5)))}, nil
	})}
	source := Source{ID: "pricing", URL: "https://example.test/pricing", Parser: "document"}
	runner := Runner{Repo: repo, Providers: sourceAliasProvider{}, Client: client}
	if err := runner.check(context.Background(), Schedule{Provider: "fixture", Time: "03:00", Sources: []Source{source}}, Settings{Timezone: "UTC"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	actual := repo.state.Observations["pricing"]
	if actual.Source.URL != source.URL+".md" || actual.Source.ID != source.ID || actual.Hash == "" || strings.TrimSpace(actual.Text) != strings.TrimSpace(strings.Repeat("Public source evidence ", 5)) {
		t.Fatal(actual)
	}
	if source.URL != "https://example.test/pricing" {
		t.Fatal("stored schedule mutated")
	}
}
