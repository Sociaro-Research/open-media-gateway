package kling

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// Synthetic unknown labels exercise the diagnostic independently of later
// reviewed model additions. Conditions mirror representative published rows;
// these fixtures never authorize a model or tariff.
var reviewUnknownRows = []string{
	"| Future Video 99.0 | No Input Video (Input videos include reference videos (feature video and base video), and video character element.) | Per second (Total duration = Output duration) | 0.6 Units ($0.094) /s | - | - |",
	"| Future Video 99.0 | With Input Video (Input Duration ≤ Output Duration) (Input videos include reference videos (feature video and base video), and video character element.) | Per second (Total duration = Output duration × 2) | 0.4 Units ($0.063) /s | - | - |",
	"| Future Video 99.0 | With Input Video (Input Duration > Output Duration) (Input videos include reference videos (feature video and base video), and video character element.) | Per second (Total duration = Input duration + Output duration) | 0.4 Units ($0.063) /s | - | - |",
}

type pricingReviewRepo struct {
	catalogsync.Repository
	run catalogsync.Run
}

func (*pricingReviewRepo) Settings(context.Context) (catalogsync.Settings, error) {
	return catalogsync.Settings{Timezone: "UTC", Version: 1}, nil
}
func (*pricingReviewRepo) Schedules(context.Context) ([]catalogsync.Schedule, error) {
	return []catalogsync.Schedule{{Provider: "kling", Time: "17:00", Requested: true, Sources: []catalogsync.Source{{ID: "pricing", URL: priceMarkdownSource, Parser: "document"}}}}, nil
}
func (*pricingReviewRepo) State(context.Context, string) (catalogsync.State, error) {
	return catalogsync.State{}, nil
}
func (r *pricingReviewRepo) Commit(_ context.Context, _ catalogsync.Schedule, _ catalogsync.State, run catalogsync.Run, _ time.Time) error {
	r.run = run
	return nil
}
func (*pricingReviewRepo) WithLock(ctx context.Context, f func(context.Context) error) (bool, error) {
	return true, f(ctx)
}

type pricingReviewProvider struct{}

func (pricingReviewProvider) CaptureCatalog(context.Context, string) (catalogsync.Capture, error) {
	return catalogsync.Capture{}, nil
}
func (pricingReviewProvider) CheckCatalogRate(context.Context, string, core.MediaModel, catalogsync.Rate) error {
	return nil
}
func (pricingReviewProvider) InterpretCatalogSource(_ context.Context, _ string, s catalogsync.Source, text string) ([]catalogsync.Entry, error) {
	entries, err := interpretPrices(s, text)
	if err != nil {
		return entries, fmt.Errorf("untrusted wrapper: %w", err)
	}
	return entries, nil
}

type pricingReviewTransport string

func (body pricingReviewTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != "GET" || r.URL.String() != priceMarkdownSource || r.Header.Get("Authorization") != "" {
		return nil, errors.New("unexpected fixture request")
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}

func TestPricingModelReviewRows(t *testing.T) {
	bytes, err := os.ReadFile("testdata/pricing.md")
	if err != nil {
		t.Fatal(err)
	}
	control := string(bytes)
	source := catalogsync.Source{ID: "pricing", URL: priceMarkdownSource, Parser: "document"}
	accepted, err := interpretPrices(source, control)
	if err != nil || len(accepted) == 0 {
		t.Fatalf("unchanged pricing control failed: %d, %v", len(accepted), err)
	}
	for i, row := range reviewUnknownRows {
		t.Run(fmt.Sprintf("unknown-row-%d", i+1), func(t *testing.T) {
			body := strings.Replace(control, "| Kling 3.0 Turbo |", row+"\n| Kling 3.0 Turbo |", 1)
			entries, err := interpretPrices(source, body)
			if err == nil || entries != nil {
				t.Fatal("unreviewed tariff accepted or partial entries leaked")
			}
			repo := &pricingReviewRepo{}
			runner := catalogsync.Runner{Repo: repo, Providers: pricingReviewProvider{}, Client: &http.Client{Transport: pricingReviewTransport(body)}}
			if err := runner.Tick(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			ob := repo.run.Sources[1]
			if ob.ModelReview == nil || ob.ModelReview.ModelLabel != "Future Video 99.0" || ob.ModelReview.Snapshot != ob.FetchedHash || ob.ModelReview.URL != source.URL || ob.ModelReview.Line != strings.Count(body[:strings.Index(body, row)], "\n")+1 {
				t.Fatalf("wrong source-bound row diagnostic: %+v", ob)
			}
			_, text, _ := catalogsync.Report(repo.run)
			if !strings.Contains(text, "Future Video 99.0") || !strings.Contains(text, "Adapter review/integration required") || strings.Contains(text, "untrusted wrapper") {
				t.Fatal("missing or unsafe report", text)
			}
		})
	}
	for _, tc := range []struct {
		name, label, condition string
		named                  bool
	}{
		{"future", "Kling 5.1 Future", "No Input Video", true},
		{"changed-condition", "Kling 4.0", "Changed Input Video condition", true},
		{"markup", "<b>Kling Flash</b>", "No Input Video", false},
		{"credential", "Bearer secret", "No Input Video", false},
		{"control-character", "Kling\t4.0 Flash", "No Input Video", false},
		{"oversize", strings.Repeat("A", 81), "No Input Video", false},
		{"malformed-columns", "Kling | Flash", "No Input Video", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := strings.Replace(reviewUnknownRows[0], "Future Video 99.0", tc.label, 1)
			row = strings.Replace(row, "No Input Video", tc.condition, 1)
			body := strings.Replace(control, "| Kling 3.0 Turbo |", row+"\n| Kling 3.0 Turbo |", 1)
			entries, err := interpretPrices(source, body)
			if err == nil || entries != nil {
				t.Fatal("unreviewed row accepted")
			}
			repo := &pricingReviewRepo{}
			runner := catalogsync.Runner{Repo: repo, Providers: pricingReviewProvider{}, Client: &http.Client{Transport: pricingReviewTransport(body)}}
			if err := runner.Tick(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			ob := repo.run.Sources[1]
			if (ob.ModelReview != nil) != tc.named {
				t.Fatalf("wrong typed/generic fallback: %+v", ob)
			}
			_, text, _ := catalogsync.Report(repo.run)
			if tc.named && !strings.Contains(text, tc.label) {
				t.Fatal("public label omitted")
			}
			if !tc.named && strings.Contains(text, tc.label) {
				t.Fatal("hostile label exposed")
			}
		})
	}
}
