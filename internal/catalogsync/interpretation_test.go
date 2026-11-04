package catalogsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func reviewSource() Source {
	return Source{ID: "pricing", URL: "https://example.test/pricing.md", Parser: "document"}
}

func TestModelReviewErrorValidation(t *testing.T) {
	source, hash := reviewSource(), strings.Repeat("a", 64)
	for _, label := range []string{"Kling 4.0 Flash", "Future Video 5.1 Pro", "Model-7.0 (Preview)"} {
		err := fmt.Errorf("untrusted wrapper secret: %w", NewModelReviewError(source, label, 42))
		e := interpretationReview(err, source, hash)
		if e == nil || e.ModelLabel != label || e.Line != 42 || e.URL != source.URL || e.Snapshot != hash {
			t.Fatalf("valid wrapped evidence lost: %+v", e)
		}
		if interpretationReview(err, Source{ID: "other", URL: source.URL, Parser: "document"}, hash) != nil {
			t.Fatal("evidence rebound to another source")
		}
		if interpretationReview(err, source, "bad") != nil {
			t.Fatal("invalid snapshot accepted")
		}
	}
	for _, label := range []string{"", " Kling Flash", "Kling Flash ", "Kling\nFlash", "Kling\tFlash", "Kling\rFlash", "<b>Kling Flash</b>", "Kling & Flash", "[Flash](https://evil.test)", "Bearer abc", "api_key abc", "secret abc", "sk-123", strings.Repeat("A", 81), "Model " + strings.Repeat("a", 32), "Kling\x00Flash", "Kling 😀"} {
		err := NewModelReviewError(source, label, 42)
		if interpretationReview(err, source, hash) != nil || strings.Contains(err.Error(), label) && label != "" {
			t.Fatalf("unsafe label escaped: %q", label)
		}
	}
	for _, line := range []int{-1, 0, 100001} {
		if interpretationReview(NewModelReviewError(source, "Kling Flash", line), source, hash) != nil {
			t.Fatal("invalid line accepted")
		}
	}
	for _, url := range []string{"http://example.test/pricing", "https://user:pass@example.test/pricing", "https://example.test/pricing?token=private", "https://example.test/pricing#fragment"} {
		bad := source
		bad.URL = url
		if interpretationReview(NewModelReviewError(bad, "Kling Flash", 2), bad, hash) != nil {
			t.Fatal("unsafe URL accepted")
		}
	}
	var nilTyped *modelReviewError
	for _, err := range []error{nil, errors.New("Kling Flash token=private"), fmt.Errorf("wrapper: %w", errors.New("private")), nilTyped} {
		if interpretationReview(err, source, hash) != nil {
			t.Fatal("untyped or nil error produced model evidence")
		}
	}
}

type reviewTransport func(*http.Request) (*http.Response, error)

func (f reviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSourceFailureStagesAndLegacy(t *testing.T) {
	source := reviewSource()
	for _, tc := range []struct {
		name   string
		source Source
		status int
		body   string
		err    error
		want   string
	}{
		{"transport", source, 0, "", errors.New("private credential"), "retrieval_failed"},
		{"http", source, 503, "private body", nil, "retrieval_failed"},
		{"parse", Source{ID: source.ID, URL: source.URL, Parser: "manifest-v1"}, 200, "malformed private JSON", nil, "interpretation_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: reviewTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, tc.err
			})}
			ob := Fetch(context.Background(), client, tc.source)
			if ob.Error == "" || failureStatus(ob) != tc.want || strings.Contains(ob.Error, "private") {
				t.Fatalf("wrong stage or leaked error: %+v", ob)
			}
			if tc.want == "interpretation_failed" && !validSnapshotHash(ob.FetchedHash) {
				t.Fatal("downloaded snapshot lost")
			}
		})
	}
	for _, tc := range []struct{ raw, want string }{
		{`{"error":"Operational source interpretation failed; last confirmed information was preserved"}`, "interpretation_failed"},
		{`{"error":"Source returned HTTP 503"}`, "retrieval_failed"},
		{`{"error":"unrecognized legacy parser error"}`, "retrieval_failed"},
		{`{"error":"anything","failure_stage":"forged"}`, "retrieval_failed"},
	} {
		var ob Observation
		if err := json.Unmarshal([]byte(tc.raw), &ob); err != nil {
			t.Fatal(err)
		}
		if failureStatus(ob) != tc.want {
			t.Fatal("legacy misclassification", tc.raw)
		}
	}
}

type reviewRepository struct {
	Repository
	state State
	run   Run
}

func (r *reviewRepository) State(context.Context, string) (State, error) {
	var s State
	_ = json.Unmarshal(encoded(r.state), &s)
	return s, nil
}
func (r *reviewRepository) Commit(_ context.Context, _ Schedule, s State, run Run, _ time.Time) error {
	r.state, r.run = s, run
	return nil
}

type reviewProvider struct {
	model core.MediaModel
	err   error
}

func (p reviewProvider) CaptureCatalog(context.Context, string) (Capture, error) {
	return Capture{Models: []core.MediaModel{p.model}}, nil
}
func (reviewProvider) CheckCatalogRate(context.Context, string, core.MediaModel, Rate) error {
	return nil
}
func (p reviewProvider) InterpretCatalogSource(context.Context, string, Source, string) ([]Entry, error) {
	// A buggy interpreter must not publish partial entries alongside an error.
	return []Entry{{ID: "unreviewed-model", Rates: []Rate{{Price: 1, Unit: "second", Per: 1}}}}, p.err
}

func TestRunnerInterpretationGenericFallback(t *testing.T) {
	source := reviewSource()
	other := source
	other.ID = "other"
	for _, err := range []error{errors.New("Bearer private"), fmt.Errorf("private wrapper: %w", errors.New("private")), NewModelReviewError(other, "Future Video", 5)} {
		repo := &reviewRepository{}
		runner := Runner{Repo: repo, Providers: reviewProvider{model: core.MediaModel{ID: "reviewed"}, err: err}, Client: &http.Client{Transport: reviewTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("public document ", 10)))}, nil
		})}}
		if err := runner.check(context.Background(), Schedule{Provider: "kling", Time: "17:00", Sources: []Source{source}}, Settings{Timezone: "UTC"}, time.Now()); err != nil {
			t.Fatal(err)
		}
		ob := repo.run.Sources[1]
		if ob.Error != interpretationError || ob.ModelReview != nil || len(ob.Entries) != 0 || ob.FailureStage != "interpretation" {
			t.Fatalf("unsafe fallback: %+v", ob)
		}
		if _, ok := repo.state.Models["unreviewed-model"]; ok {
			t.Fatal("partial interpreter entries published")
		}
		_, text, _ := Report(repo.run)
		if strings.Contains(text, "private") || strings.Contains(text, "Future Video") {
			t.Fatal("untyped/cross-source evidence escaped", text)
		}
	}
}

func TestInterpretationRunnerWarningLifecycle(t *testing.T) {
	source := reviewSource()
	model := core.MediaModel{ID: "reviewed-model", MediaType: "video"}
	entry := Entry{ID: model.ID, Contract: &model, Rates: []Rate{{Unit: "second", Per: 1, Price: 123, Region: "global", Source: source.URL}}}
	initial := State{Version: 2, Models: map[string]Entry{model.ID: entry}, Observations: map[string]Observation{source.ID: {Source: source, Hash: strings.Repeat("b", 64), Entries: []Entry{entry}}}}
	repo := &reviewRepository{state: initial}
	provider := reviewProvider{model: model, err: fmt.Errorf("private wrapper: %w", NewModelReviewError(source, "Kling 4.0 Flash", 9))}
	runner := Runner{Repo: repo, Providers: provider, Client: &http.Client{Transport: reviewTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("public document ", 10)))}, nil
	})}}
	now := time.Date(2026, 11, 4, 17, 0, 0, 0, time.UTC)
	var first Warning
	for day := 0; day < 2; day++ {
		if err := runner.check(context.Background(), Schedule{Provider: "kling", Time: "17:00", Sources: []Source{source}}, Settings{Timezone: "UTC", Email: "offline@example.invalid"}, now.Add(time.Duration(day)*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if repo.state.Version != initial.Version || !reflect.DeepEqual(repo.state.Models, initial.Models) || len(repo.run.Changes) != 0 {
			t.Fatal("failed interpretation changed published state")
		}
		if repo.state.Observations[source.ID].Hash != initial.Observations[source.ID].Hash {
			t.Fatal("last confirmed snapshot replaced")
		}
		var warning *Warning
		for i := range repo.run.OpenWarnings {
			if repo.run.OpenWarnings[i].Reason == sourceFailureReason {
				warning = &repo.run.OpenWarnings[i]
			}
		}
		if warning == nil || warning.Model != "" || warning.Key != "|pricing|"+sourceFailureReason || warning.Occurrences != day+1 {
			t.Fatalf("warning identity/history changed: %+v", warning)
		}
		if day == 0 {
			first = *warning
		} else if warning.FirstSeen != first.FirstSeen || len(repo.run.NewWarnings) != 0 {
			t.Fatal("repeat reset history or duplicated alert")
		}
		_, text, markup := Report(repo.run)
		for _, want := range []string{"Kling 4.0 Flash", "Adapter review/integration required", "support and entitlement are unverified", source.URL, `"line":9`, "interpretation_failed"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing actionable evidence %q: %s", want, text)
			}
		}
		if strings.Contains(text, "private wrapper") || strings.Contains(markup, "private wrapper") || repo.run.EmailStatus != "pending" {
			t.Fatal("unsafe or missing report")
		}
		if repo.run.Completeness.Sources[1].Status != "interpretation_failed" {
			t.Fatalf("wrong source status: %+v", repo.run.Completeness.Sources)
		}
	}
	// A legacy error warning is enriched without replacing its identity/first seen.
	legacy := first
	legacy.Evidence = encoded(map[string]string{"error": interpretationError, "hash": repo.run.Sources[1].FetchedHash})
	legacy.Occurrences = 12
	repo.state.Warnings = []Warning{legacy}
	if err := runner.check(context.Background(), Schedule{Provider: "kling", Time: "17:00", Sources: []Source{source}}, Settings{Timezone: "UTC"}, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(repo.run.OpenWarnings) != 1 || repo.run.OpenWarnings[0].FirstSeen != first.FirstSeen || repo.run.OpenWarnings[0].Occurrences != 13 {
		t.Fatal("legacy warning history discarded")
	}
	// A confirmed successful observation resolves only the source failure; no
	// integration claim or previous actionable request is printed as still open.
	ob := Observation{Source: source, Hash: initial.Observations[source.ID].Hash, Entries: []Entry{entry}}
	_, resolved := Reconcile(repo.state, Capture{Models: []core.MediaModel{model}}, []Observation{ob}, now.Add(3*time.Hour), func(core.MediaModel, Rate) error { return nil })
	if len(resolved.ResolvedWarnings) != 1 || resolved.ResolvedWarnings[0].Key != first.Key || resolved.ResolvedWarnings[0].ResolvedAt == nil {
		t.Fatal("warning not resolved normally")
	}
	_, text, _ := Report(resolved)
	if strings.Contains(text, "Adapter review/integration required") {
		t.Fatal("resolved warning presented as ongoing adapter work")
	}
}

func TestInterpretationReportRejectsForgedAndLegacyEvidence(t *testing.T) {
	source, hash := reviewSource(), strings.Repeat("a", 64)
	valid := &ModelReviewEvidence{SourceID: source.ID, URL: source.URL, Snapshot: hash, ModelLabel: "Kling 4.0 Flash", Line: 3}
	matchingObservation := Observation{Source: source, Error: interpretationError, FailureStage: "interpretation", FetchedHash: hash, ModelReview: valid}
	validWarning := Warning{Source: source.ID, Reason: sourceFailureReason, Evidence: encoded(sourceFailureEvidence{Error: interpretationError, Hash: hash, Stage: "interpretation_failed", Review: valid})}
	if !strings.Contains(string(reportWarningEvidence(validWarning, []Observation{matchingObservation})), "Kling 4.0 Flash") {
		t.Fatal("matching typed provenance lost")
	}
	for _, tc := range []struct {
		name   string
		review *ModelReviewEvidence
	}{
		{"absent", nil},
		{"label", &ModelReviewEvidence{SourceID: source.ID, URL: source.URL, Snapshot: hash, ModelLabel: "Different Model", Line: 3}},
		{"line", &ModelReviewEvidence{SourceID: source.ID, URL: source.URL, Snapshot: hash, ModelLabel: "Kling 4.0 Flash", Line: 4}},
	} {
		t.Run("observation-"+tc.name, func(t *testing.T) {
			ob := matchingObservation
			ob.ModelReview = tc.review
			if strings.Contains(string(reportWarningEvidence(validWarning, []Observation{ob})), "model_review") {
				t.Fatal("warning label promoted without matching typed observation")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*sourceFailureEvidence)
	}{
		{"markup", func(e *sourceFailureEvidence) { e.Review.ModelLabel = "<script>private</script>" }},
		{"credential", func(e *sourceFailureEvidence) { e.Review.ModelLabel = "Bearer private" }},
		{"URL", func(e *sourceFailureEvidence) { e.Review.URL = "https://evil.test/?token=private" }},
		{"snapshot", func(e *sourceFailureEvidence) { e.Review.Snapshot = strings.Repeat("b", 64) }},
		{"source", func(e *sourceFailureEvidence) { e.Review.SourceID = "other" }},
		{"line", func(e *sourceFailureEvidence) { e.Review.Line = 0 }},
		{"stage", func(e *sourceFailureEvidence) { e.Stage = "retrieval_failed" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *valid
			e := sourceFailureEvidence{Error: "private arbitrary upstream response", Hash: hash, Stage: "interpretation_failed", Review: &copy, Action: "private action"}
			tc.mutate(&e)
			w := Warning{Source: source.ID, Reason: sourceFailureReason, Evidence: encoded(e)}
			for _, isNew := range []bool{true, false} {
				run := Run{Sources: []Observation{matchingObservation}, OpenWarnings: []Warning{w}}
				if isNew {
					run.NewWarnings = []Warning{w}
				}
				_, text, markup := Report(run)
				if strings.Contains(text, "private") || strings.Contains(text, "Kling 4.0 Flash") || strings.Contains(markup, "private") || strings.Contains(text, "Adapter review/integration") {
					t.Fatal("forged evidence escaped", text)
				}
			}
		})
	}
	for _, raw := range []string{`{"error":"Bearer private","hash":"<private>"}`, `{"error":"Source returned HTTP 503","hash":""}`, `malformed private json`} {
		w := Warning{Source: source.ID, Reason: sourceFailureReason, Evidence: json.RawMessage(raw)}
		got := string(reportWarningEvidence(w, nil))
		if strings.Contains(got, "private") || !json.Valid([]byte(got)) {
			t.Fatal("unsafe legacy fallback", got)
		}
	}
	for _, field := range []string{"error", "action"} {
		// A type error must not allow json.Unmarshal's partially decoded valid
		// review fields to be promoted into actionable evidence.
		e := sourceFailureEvidence{Error: interpretationError, Hash: hash, Stage: "interpretation_failed", Review: valid, Action: "review"}
		var raw map[string]any
		_ = json.Unmarshal(encoded(e), &raw)
		raw[field] = 123
		w := Warning{Source: source.ID, Reason: sourceFailureReason, Evidence: encoded(raw)}
		got := string(reportWarningEvidence(w, []Observation{matchingObservation}))
		if strings.Contains(got, "Kling 4.0 Flash") || strings.Contains(got, "model_review") || strings.Contains(got, "action") {
			t.Fatal("malformed object retained partially decoded review", got)
		}
	}
}
