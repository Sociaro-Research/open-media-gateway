package catalogsync

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func accepted(core.MediaModel, Rate) error { return nil }
func TestReconciliationLifecycle(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	model := core.MediaModel{ID: "seedance", MediaType: "video", Metadata: &core.ModelMetadata{VersionDate: "2026-06-28"}}
	capture := Capture{Models: []core.MediaModel{model}, IDs: []string{"seedance", "new-model"}}
	source := Source{ID: "releases", URL: "https://example.test/releases", Parser: "manifest-v1"}
	ob := Observation{Source: source, Hash: "one", Entries: []Entry{{ID: "seedance", Metadata: &core.ModelMetadata{ReleaseDate: "2026-06"}}, {ID: "new-model", Metadata: &core.ModelMetadata{ReleaseDate: "2026-10-09"}}}}
	state, run := Reconcile(State{}, capture, []Observation{ob}, now, accepted)
	if state.Version != 1 || state.Models["seedance"].Metadata.ReleaseDate != "2026-06" || len(run.NewWarnings) != 2 || run.EmailStatus != "pending" {
		t.Fatalf("initial import: %+v", run)
	}
	quiet, run := Reconcile(state, capture, []Observation{ob}, now.Add(24*time.Hour), accepted)
	if quiet.Version != 1 || len(run.Changes) != 0 || len(run.NewWarnings) != 0 || run.EmailStatus != "not_needed" || run.OpenWarnings[0].Occurrences != 2 {
		t.Fatalf("quiet day: %+v", run)
	}
	capture.Models = append(capture.Models, core.MediaModel{ID: "new-model"})
	fixed, run := Reconcile(quiet, capture, []Observation{ob}, now.Add(48*time.Hour), accepted)
	if len(fixed.Warnings) != 0 || len(run.ResolvedWarnings) != 2 || fixed.Models["new-model"].Metadata.ReleaseDate != "2026-10-09" || fixed.Version != 2 {
		t.Fatalf("code upgrade without upstream changes: %+v", run)
	}
	if state.Models["seedance"].Metadata.ReleaseDate != "2026-06" {
		t.Fatal("previous snapshot mutated")
	}
}
func TestFailureDoesNotResolveOrDeleteEvidence(t *testing.T) {
	now := time.Now().UTC()
	source := Source{ID: "source", URL: "https://example.test/source", Parser: "manifest-v1"}
	ob := Observation{Source: source, Hash: "one", Entries: []Entry{{ID: "unknown"}}}
	state, _ := Reconcile(State{}, Capture{}, []Observation{ob}, now, accepted)
	next, run := Reconcile(state, Capture{}, []Observation{{Source: source, Error: "HTTP 503"}}, now.Add(time.Hour), accepted)
	if len(next.Warnings) != 2 || len(run.ResolvedWarnings) != 0 || next.Observations["source"].Hash != "one" {
		t.Fatalf("failure erased pending evidence: %+v", run)
	}
	_, run = Reconcile(next, Capture{}, nil, now.Add(2*time.Hour), accepted)
	if len(run.ResolvedWarnings) != 0 {
		t.Fatal("removing a source silently resolved warnings")
	}
}

func TestDiscoveredUnreviewedVersionIsNotReportedAsAbsent(t *testing.T) {
	const id = "seedream-4-0-20270101"
	model := core.MediaModel{ID: id, MediaType: "image"}
	previous := State{Models: map[string]Entry{id: {ID: id, Contract: &model}}}
	_, run := Reconcile(previous, Capture{IDs: []string{id}}, []Observation{{Source: Source{ID: "_api"}}}, time.Now(), accepted)
	if len(run.OpenWarnings) != 1 || run.OpenWarnings[0].Reason != "Model requires an adapter contract before it can be enabled" {
		t.Fatalf("a discovered model must only require review, not be reported absent: %+v", run.OpenWarnings)
	}
}
func TestConflictingSourcesDoNotOscillate(t *testing.T) {
	model := core.MediaModel{ID: "m"}
	capture := Capture{Models: []core.MediaModel{model}}
	obs := []Observation{{Source: Source{ID: "one"}, Entries: []Entry{{ID: "m", Metadata: &core.ModelMetadata{ReleaseDate: "2026-06", Description: "one"}}}}, {Source: Source{ID: "two"}, Entries: []Entry{{ID: "m", Metadata: &core.ModelMetadata{ReleaseDate: "2026-07", Description: "two"}}}}}
	state, _ := Reconcile(State{}, capture, obs, time.Now(), accepted)
	_, run := Reconcile(state, capture, obs, time.Now(), accepted)
	if len(run.Changes) != 0 || len(run.NewWarnings) != 0 {
		t.Fatalf("source conflict oscillated: %+v", run)
	}
}
func TestTariffCompatibilityAndImmutableTaskSnapshot(t *testing.T) {
	old := core.TaskCost{EstimatedUnits: 100, ExpectedUSD: 100 * money.USD(money.Scale), Pricing: core.PricingSnapshot{Currency: "USD", Unit: "token", Per: 1, Price: money.USD(money.Scale), Region: "Singapore"}}
	updated, err := Reprice(old, Rate{Unit: "token", Per: 1, Price: 2 * money.USD(money.Scale), Region: "Singapore"})
	if err != nil || updated.ExpectedUSD != 200*money.USD(money.Scale) || old.Pricing.Price != money.USD(money.Scale) {
		t.Fatalf("repricing: %+v %v", updated, err)
	}
	for _, rate := range []Rate{{Unit: "second", Per: 1, Region: "Singapore"}, {Unit: "token", Per: 1, Region: "Beijing"}, {Unit: "token", Per: 0, Region: "Singapore"}, {Unit: "token", Per: 1, Price: -1, Region: "Singapore"}} {
		if _, err := Reprice(old, rate); err == nil {
			t.Fatal("accepted incompatible tariff", rate)
		}
	}
	old.Pricing.Unit = "multi_metric"
	old.Pricing.Components = []core.PricingComponent{{Metric: "text", Per: 100, Price: money.USD(money.Scale), EstimatedUnits: 100}}
	updated, err = Reprice(old, Rate{Unit: "multi_metric", Per: 1, Region: "Singapore", Components: []core.PricingComponent{{Metric: "text", Per: 100, Price: 2 * money.USD(money.Scale)}}})
	if err != nil || updated.ExpectedUSD != 2*money.USD(money.Scale) || old.Pricing.Components[0].Price != money.USD(money.Scale) {
		t.Fatal("multi metric snapshot mutated", err)
	}
}
func TestCalendarScheduleAcrossDST(t *testing.T) {
	for _, now := range []time.Time{time.Date(2026, 3, 28, 23, 30, 0, 0, time.UTC), time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC)} {
		next := NextDaily(now, "06:30", "Asia/Nicosia")
		loc, _ := time.LoadLocation("Asia/Nicosia")
		if !next.After(now) || next.In(loc).Hour() != 6 || next.In(loc).Minute() != 30 {
			t.Fatal("DST schedule", next)
		}
	}
	for _, settings := range []Settings{{Timezone: "", Version: 1}, {Timezone: "Local", Version: 1}, {Timezone: "bad/zone", Version: 1}, {Timezone: "UTC", Email: "a@example.test\r\nBcc:b@example.test", Version: 1}} {
		if settings.Validate() == nil {
			t.Fatal("invalid settings accepted")
		}
	}
}
func TestSourceParsers(t *testing.T) {
	for _, tc := range []struct{ parser, text, id, date string }{
		{"byteplus-releases", "# 202606\n|ByteDance |dreamina\\-seedance\\-2\\-5\\-260628 |Video generation model |New release |Long stories |", "dreamina-seedance-2-5-260628", "2026-06"},
		{"alibaba-releases", "<html><main><table><tr><td>Image generation</td><td>2026-10-02</td><td>International</td><td>qwen-image-2.1-pro</td><td>New model</td></tr></table></main></html>", "qwen-image-2.1-pro", "2026-10-02"},
	} {
		entries, _, err := Parse(Source{Parser: tc.parser}, []byte(tc.text))
		if err != nil || len(entries) != 1 || entries[0].ID != tc.id || entries[0].Metadata.ReleaseDate != tc.date {
			t.Fatalf("%s: %v %+v", tc.parser, err, entries)
		}
	}
	entries, _, err := Parse(Source{Parser: "byteplus-pricing"}, []byte("|Model ID|Online inference (USD / M tokens)|Offline inference (USD / M tokens)|\n|dreamina-seedance-2-5-260628 |* For 480p and 720p outputs:<br>* Input without video: 10.70<br>* Input with video: 6.40 |Not supported yet |"))
	if err != nil || len(entries) != 1 || len(entries[0].Rates) != 4 || entries[0].Rates[0].Price != 10_700_000 {
		t.Fatal("token tariff", entries, err)
	}
	for _, parser := range []string{"byteplus-releases", "byteplus-models", "byteplus-pricing", "alibaba-releases", "openai-pricing", "openai-model"} {
		if _, _, err := Parse(Source{Parser: parser, URL: "https://example.test/model"}, []byte("<html>Sign in</html>")); err == nil {
			t.Fatal("missing content accepted", parser)
		}
	}
	if _, _, err := Parse(Source{Parser: "manifest-v1"}, []byte(`{"models":[]} {"models":[]}`)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}
func TestReportIncludesOldWarningsAndEscapesHTML(t *testing.T) {
	run := Run{OpenWarnings: []Warning{{Key: "old", Reason: "<script>alert(1)</script>", Occurrences: 3}}, NewWarnings: []Warning{{Key: "new", Reason: "new"}}}
	_, text, markup := Report(run)
	if !strings.Contains(text, "checks: 3") || strings.Contains(markup, "<script>") || !strings.Contains(markup, "ACTION REQUIRED") {
		t.Fatal("report missing/unsafe", markup)
	}
}

func TestManifestRequiresExplicitTariffPrices(t *testing.T) {
	for _, tc := range []struct {
		name, rate string
		valid      bool
	}{
		{"missing scalar", `{"unit":"image","per":1}`, false},
		{"null scalar", `{"unit":"image","per":1,"price":null}`, false},
		{"explicit free scalar", `{"unit":"image","per":1,"price":0}`, true},
		{"missing reference price", `{"unit":"image","per":1,"price":0.04,"input_images":{"free":1}}`, false},
		{"free reference price", `{"unit":"image","per":1,"price":0.04,"input_images":{"free":1,"price":0}}`, true},
		{"missing component", `{"unit":"multi_metric","per":1,"components":[{"metric":"text","per":1000000}]}`, false},
		{"null component", `{"unit":"multi_metric","per":1,"components":[{"metric":"text","per":1000000,"price":null}]}`, false},
		{"explicit free component", `{"unit":"multi_metric","per":1,"components":[{"metric":"text","per":1000000,"price":0}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Parse(Source{Parser: "manifest-v1"}, []byte(`{"models":[{"id":"m","rates":[`+tc.rate+`]}]}`))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

// Optional read-only acceptance against previously downloaded official pages.
// Ordinary unit tests use the self-contained provider-format fixtures above.
func TestDownloadedOfficialSources(t *testing.T) {
	dir := os.Getenv("GATEWAY_CATALOG_REVIEW_DIR")
	if dir == "" {
		t.Skip("no downloaded official source snapshot")
	}
	for _, tc := range []struct{ file, parser string }{{"byte-releases.html", "byteplus-releases"}, {"byte-models.html", "byteplus-models"}, {"byte-pricing.html", "byteplus-pricing"}, {"alibaba-releases.html", "alibaba-releases"}, {"alibaba-pricing.html", "alibaba-pricing"}, {"openai-pricing.html", "openai-pricing"}, {"openai-model.html", "openai-model"}} {
		body, err := os.ReadFile(filepath.Join(dir, tc.file))
		if err != nil {
			t.Fatal(err)
		}
		entries, _, err := Parse(Source{Parser: tc.parser, URL: "https://developers.openai.com/api/docs/models/gpt-image-2.5-flare"}, body)
		if err != nil || len(entries) == 0 {
			t.Fatalf("%s: %v", tc.file, err)
		}
		if tc.parser == "byteplus-releases" {
			found := false
			for _, m := range entries {
				if m.ID == "dreamina-seedance-2-5-260628" {
					found = m.Metadata.ReleaseDate == "2026-06"
				}
			}
			if !found {
				t.Fatal("Seedance release missing")
			}
		}
		t.Logf("%s: %d records", tc.file, len(entries))
		if tc.parser == "openai-pricing" {
			t.Log(string(encoded(entries[:1])))
		}
	}
}

func TestDocumentationWarningsPersistAndAdapterDescriptorsRemainImmutable(t *testing.T) {
	now := time.Now().UTC()
	native := Capture{Models: []core.MediaModel{{ID: "m", Metadata: &core.ModelMetadata{Description: "Compiled"}}}}
	source := Source{ID: "model", Parser: "openai-model", URL: "https://example.test/models/m"}
	original := Observation{Source: source, Hash: "one", Text: "initial", Entries: []Entry{{ID: "m", Metadata: &core.ModelMetadata{Description: "Published"}}}}
	state, _ := Reconcile(State{}, native, []Observation{original}, now, accepted)
	if native.Models[0].Metadata.Description != "Compiled" {
		t.Fatal("adapter descriptor mutated")
	}
	original.Hash = "two"
	state, run := Reconcile(state, native, []Observation{original}, now.Add(time.Hour), accepted)
	if len(run.NewWarnings) != 1 {
		t.Fatal("unparsed model documentation change missing", run)
	}
	_, run = Reconcile(state, native, []Observation{original}, now.Add(2*time.Hour), accepted)
	if len(run.OpenWarnings) != 1 || len(run.NewWarnings) != 0 || len(run.ResolvedWarnings) != 0 || run.EmailStatus != "not_needed" {
		t.Fatal("documentation warning disappeared or repeated email", run)
	}
}
func TestUnsupportedTariffsRetainEverySelectorAndResolveAfterCodeSupport(t *testing.T) {
	native := Capture{Models: []core.MediaModel{{ID: "m"}}}
	ob := Observation{Source: Source{ID: "pricing"}, Entries: []Entry{{ID: "m", Rates: []Rate{{Region: "Singapore", Resolution: "720p", Unit: "second", Per: 1000}, {Region: "Beijing", Resolution: "1080p", Unit: "second", Per: 1000}}}}}
	state, run := Reconcile(State{}, native, []Observation{ob}, time.Now(), func(core.MediaModel, Rate) error { return core.ErrPricingUnavailable })
	if len(run.OpenWarnings) != 2 {
		t.Fatal("regional variants were overwritten", run)
	}
	state, run = Reconcile(state, native, []Observation{ob}, time.Now(), accepted)
	if len(state.Models["m"].Rates) != 2 || len(run.ResolvedWarnings) != 2 || len(run.OpenWarnings) != 0 {
		t.Fatal("code upgrade failed to apply unchanged tariff evidence", run)
	}
}
func TestPublishedTariffUnitsMustBeExplicit(t *testing.T) {
	for _, unit := range []string{"USD / K tokens", "CNY / M tokens", ""} {
		entries, _, err := Parse(Source{Parser: "byteplus-pricing"}, []byte("|Model ID|Online inference ("+unit+")|Offline inference|\n|dreamina-seedance-2-5-260628|For 720p outputs: Input without video: 10.70|Not supported yet|"))
		if err != nil || len(entries) != 1 || len(entries[0].Rates) != 0 {
			t.Fatal("unconfirmed billing unit accepted", unit, entries, err)
		}
	}
}
func TestAPIOutageRetainsPreviousModelWarnings(t *testing.T) {
	now := time.Now()
	capture := Capture{Models: []core.MediaModel{{ID: "known"}}, IDs: []string{"known", "new"}}
	source := Source{ID: "_api", Parser: "api"}
	state, _ := Reconcile(State{}, capture, []Observation{{Source: source, Hash: "one"}}, now, accepted)
	capture.IDs = nil
	next, run := Reconcile(state, capture, []Observation{{Source: source, Error: "Unavailable"}}, now.Add(time.Hour), accepted)
	if len(next.Warnings) != 2 || len(run.ResolvedWarnings) != 0 {
		t.Fatal("API outage resolved unreviewed model", run)
	}
}

func TestSkippedAndRepeatedLocalHourRunsOnce(t *testing.T) {
	spring := NextDaily(time.Date(2026, 3, 28, 23, 0, 0, 0, time.UTC), "03:30", "Asia/Nicosia")
	if !spring.Equal(time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC)) {
		t.Fatal("skipped hour did not use first valid time", spring)
	}
	autumn := NextDaily(time.Date(2026, 10, 25, 0, 45, 0, 0, time.UTC), "03:30", "Asia/Nicosia")
	if !autumn.Equal(time.Date(2026, 10, 26, 1, 30, 0, 0, time.UTC)) {
		t.Fatal("repeated hour ran twice", autumn)
	}
}

func TestConflictingOrRemovedTariffsPreserveLastConfirmedPrice(t *testing.T) {
	native := Capture{Models: []core.MediaModel{{ID: "m"}}}
	rate := Rate{Region: "Singapore", Unit: "image", Per: 1, Price: money.USD(money.Scale)}
	one := Observation{Source: Source{ID: "one", URL: "https://example.test/one"}, Entries: []Entry{{ID: "m", Rates: []Rate{rate}}}}
	state, _ := Reconcile(State{}, native, []Observation{one}, time.Now(), accepted)
	rate.Price *= 2
	two := Observation{Source: Source{ID: "two", URL: "https://example.test/two"}, Entries: []Entry{{ID: "m", Rates: []Rate{rate}}}}
	state, run := Reconcile(state, native, []Observation{one, two}, time.Now(), accepted)
	if len(run.NewWarnings) != 2 || len(run.Changes) != 0 || state.Models["m"].Rates[0].Price != money.USD(money.Scale) {
		t.Fatal("conflicting tariffs alternated", run)
	}
	_, run = Reconcile(state, native, []Observation{one, two}, time.Now(), accepted)
	if len(run.Changes) != 0 || len(run.NewWarnings) != 0 {
		t.Fatal("tariff conflict repeated email", run)
	}
	one.Entries = []Entry{{ID: "m"}}
	state, run = Reconcile(state, native, []Observation{one}, time.Now(), accepted)
	found := false
	for _, w := range run.OpenWarnings {
		if strings.HasPrefix(w.Reason, "Previously confirmed tariff") {
			found = true
		}
	}
	if !found || len(state.Models["m"].Rates) != 1 {
		t.Fatal("missing tariff silently deleted/ignored", run)
	}
}

type failedFormatTransport struct{}

func (failedFormatTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Accept") != "text/html" {
		panic("structured HTML must not negotiate Markdown")
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("<html><main>Provider changed to an unknown document layout.</main></html>")), Header: http.Header{}}, nil
}
func TestFailedFormatRetainsFetchEvidenceAndConfirmedCandidates(t *testing.T) {
	source := Source{ID: "pricing", URL: "https://example.test/pricing", Parser: "openai-pricing"}
	ob := Fetch(context.Background(), &http.Client{Transport: failedFormatTransport{}}, source)
	if ob.Error == "" || ob.FetchedHash == "" || ob.Text == "" {
		t.Fatal("failed format evidence discarded", ob)
	}
	old := State{Observations: map[string]Observation{"pricing": {Source: source, Hash: "confirmed", Entries: []Entry{{ID: "unknown"}}}}}
	state, run := Reconcile(old, Capture{}, []Observation{ob}, time.Now(), accepted)
	if state.Observations["pricing"].Hash != "confirmed" || state.Observations["pricing"].FetchedHash != ob.FetchedHash || len(run.OpenWarnings) != 2 {
		t.Fatal("failed fetch lost either candidate or new evidence", run)
	}
}
