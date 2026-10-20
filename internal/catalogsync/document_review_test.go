package catalogsync

import (
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

func TestDocumentReviewResolvesOnlyExactSuccessfulSnapshot(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	source := Source{ID: "pricing", URL: "https://example.test/pricing", Parser: "document"}
	previous := State{Observations: map[string]Observation{source.ID: {Source: source, Hash: "before"}}}
	ob := Observation{Source: source, Hash: "reviewed"}
	state, warning := Reconcile(previous, Capture{}, []Observation{ob}, now, accepted)
	if len(warning.OpenWarnings) != 1 {
		t.Fatal("unparsed edit did not warn")
	}
	review := DocumentReview{Hash: ob.Hash, Date: "2026-10-14", Reason: "Only text-model prices changed; media prices are unchanged."}
	capture := Capture{DocumentReviews: map[string]DocumentReview{source.URL: review}}
	next, fixed := Reconcile(state, capture, []Observation{ob}, now, accepted)
	if len(fixed.OpenWarnings) != 0 || len(fixed.ResolvedWarnings) != 1 || fixed.EmailStatus != "pending" || len(fixed.Changes) != 0 || next.Version != state.Version {
		t.Fatal("review did not resolve the warning independently of catalogue publication", fixed)
	}
	if next.Observations[source.ID].Review == nil || fixed.Sources[0].Review == nil || fixed.Sources[0].Review.Reason != review.Reason {
		t.Fatal("review evidence missing from state or journal")
	}
	_, report, _ := Report(fixed)
	if !strings.Contains(report, review.Reason) || !strings.Contains(report, review.Hash) || !strings.Contains(report, source.URL) {
		t.Fatal("email report omitted the review explanation")
	}
	_, repeat := Reconcile(next, capture, []Observation{ob}, now, accepted)
	if len(repeat.OpenWarnings) != 0 || repeat.EmailStatus != "not_needed" {
		t.Fatal("review repeats daily", repeat)
	}
	for _, tc := range []struct {
		name string
		ob   Observation
	}{
		{"new hash", Observation{Source: source, Hash: "later", Review: &review}},
		{"different URL", Observation{Source: Source{ID: source.ID, URL: "https://other.test/pricing", Parser: "document"}, Hash: ob.Hash, Review: &review}},
		{"fetch failure", Observation{Source: source, Hash: ob.Hash, Error: "HTTP 503", Review: &review}},
		{"structured parser", Observation{Source: Source{ID: source.ID, URL: source.URL, Parser: "openai-model"}, Hash: ob.Hash, Review: &review}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updated, run := Reconcile(state, capture, []Observation{tc.ob}, now, accepted)
			if len(run.OpenWarnings) == 0 || updated.Observations[source.ID].Review != nil || run.Sources[0].Review != nil {
				t.Fatal("review accepted unrelated or unsuccessful evidence", run)
			}
		})
	}
	// A future edit also warns after the acknowledged warning was closed.
	ob.Hash = "later"
	_, later := Reconcile(next, capture, []Observation{ob}, now, accepted)
	if len(later.NewWarnings) != 1 {
		t.Fatal("new edit silently accepted after review")
	}
}

func TestUnreviewedDocumentChangeSurvivesFetchFailureAndRecovery(t *testing.T) {
	now := time.Date(2026, 10, 16, 1, 0, 35, 0, time.UTC)
	for _, parser := range []string{"document", "openai-model"} {
		t.Run(parser, func(t *testing.T) {
			source := Source{ID: "changelog", URL: "https://example.test/changelog", Parser: parser}
			before := State{Version: 2, Observations: map[string]Observation{source.ID: {Source: source, Hash: "before"}}}
			changed := Observation{Source: source, Hash: "unreviewed"}
			state, initial := Reconcile(before, Capture{}, []Observation{changed}, now, accepted)
			if len(initial.NewWarnings) != 1 {
				t.Fatal("missing initial document-change warning", initial)
			}
			warning := initial.NewWarnings[0]
			for i, failed := range []Observation{
				{Source: source, Error: "Source returned HTTP 503"},
				{Source: source, Error: "Source format changed", FetchedHash: "failed-layout", Text: "Unknown layout"},
			} {
				var run Run
				state, run = Reconcile(state, Capture{}, []Observation{failed}, now.Add(time.Duration(i+1)*time.Hour), accepted)
				if len(run.OpenWarnings) != 2 || len(run.ResolvedWarnings) != 0 || len(run.Changes) != 0 || state.Version != 2 {
					t.Fatal("failed fetch resolved unreviewed change or altered catalogue", run)
				}
				found := false
				for _, w := range run.OpenWarnings {
					if w.Key == warning.Key {
						found = sameJSON(w.Evidence, warning.Evidence) && w.FirstSeen.Equal(warning.FirstSeen) && w.Occurrences == i+2
					}
				}
				if !found {
					t.Fatal("original document-change evidence lost", run)
				}
			}
			state, recovered := Reconcile(state, Capture{}, []Observation{changed}, now.Add(3*time.Hour), accepted)
			if len(recovered.OpenWarnings) != 1 || recovered.OpenWarnings[0].Key != warning.Key || !sameJSON(recovered.OpenWarnings[0].Evidence, warning.Evidence) || len(recovered.ResolvedWarnings) != 1 || len(recovered.NewWarnings) != 0 {
				t.Fatal("fetch recovery silently acknowledged the document change", recovered)
			}
			_, repeat := Reconcile(state, Capture{}, []Observation{changed}, now.Add(4*time.Hour), accepted)
			if len(repeat.OpenWarnings) != 1 || len(repeat.NewWarnings) != 0 || repeat.EmailStatus != "not_needed" {
				t.Fatal("unchanged unresolved evidence repeatedly notified", repeat)
			}
			// Only a successful exact review may close the remaining document warning.
			if parser == "document" {
				capture := Capture{DocumentReviews: map[string]DocumentReview{source.URL: {Hash: changed.Hash, Date: "2026-10-16", Reason: "Synthetic regression review"}}}
				_, reviewed := Reconcile(state, capture, []Observation{changed}, now.Add(5*time.Hour), accepted)
				if len(reviewed.OpenWarnings) != 0 || len(reviewed.ResolvedWarnings) != 1 {
					t.Fatal("exact review cannot resolve preserved warning", reviewed)
				}
			}
		})
	}
}

func TestOfflineDescriptorsPreserveLastAPIFactsWithoutHidingUpdates(t *testing.T) {
	now := time.Now()
	base := core.MediaModel{ID: "image", MediaType: "image", Metadata: &core.ModelMetadata{Facts: []core.ModelFact{{Label: "Provider: Output modalities", Value: "image"}}}}
	live := base
	live.Metadata = &core.ModelMetadata{Facts: append(append([]core.ModelFact{}, base.Metadata.Facts...), core.ModelFact{Label: "API output modalities", Value: "Image"})}
	capture := Capture{Models: []core.MediaModel{live}, IDs: []string{base.ID}}
	state, _ := Reconcile(State{}, capture, nil, now, accepted)
	capture.Models = []core.MediaModel{base}
	state, offline := Reconcile(state, capture, nil, now, accepted)
	if len(offline.Changes) != 0 || offline.EmailStatus != "not_needed" || len(state.Models[base.ID].Metadata.Facts) != 2 {
		t.Fatal("offline descriptor removed API evidence", offline)
	}
	capture.Models = []core.MediaModel{live}
	_, recovered := Reconcile(state, capture, nil, now, accepted)
	if len(recovered.Changes) != 0 {
		t.Fatal("API recovery falsely republished facts", recovered)
	}
	live.Metadata.Facts[1].Value = ""
	capture.Models = []core.MediaModel{live}
	changed, update := Reconcile(state, capture, nil, now, accepted)
	if len(update.Changes) != 1 || changed.Models[base.ID].Metadata.Facts[1].Value != "" || state.Models[base.ID].Metadata.Facts[1].Value != "Image" {
		t.Fatal("explicit API update lost or previous version mutated", update)
	}
}

func TestReviewedShutdownBackfillKeepsConfirmedAndStructuredDates(t *testing.T) {
	for _, tc := range []struct {
		name, previousDate, structuredDate, reviewedDate, want string
		unreviewed, missingMetadata                            bool
	}{
		{name: "missing date", reviewedDate: "2027-01-06", want: "2027-01-06"},
		{name: "missing metadata", missingMetadata: true, reviewedDate: "2027-01-06", want: "2027-01-06"},
		{name: "confirmed date", previousDate: "2027-02-01", reviewedDate: "2027-01-06", want: "2027-02-01"},
		{name: "structured update", structuredDate: "2027-03-01", reviewedDate: "2027-01-06", want: "2027-03-01"},
		{name: "unreviewed descriptor", unreviewed: true, reviewedDate: "2027-01-06"},
		{name: "invalid review date", reviewedDate: "2027-13-06"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
			base := core.MediaModel{ID: "speech", MediaType: "speech"}
			capture := Capture{Models: []core.MediaModel{base}, IDs: []string{base.ID}}
			state, _ := Reconcile(State{}, capture, nil, now, accepted)
			entry := state.Models[base.ID]
			if !tc.missingMetadata {
				entry.Metadata = &core.ModelMetadata{ShutdownDate: tc.previousDate}
			}
			state.Models[base.ID] = entry
			reviewed := base
			reviewed.Metadata = &core.ModelMetadata{ShutdownDate: tc.reviewedDate}
			if tc.unreviewed {
				capture.Models = []core.MediaModel{reviewed}
			} else {
				capture.Reviewed = []core.MediaModel{reviewed}
			}
			var observations []Observation
			if tc.structuredDate != "" {
				observations = []Observation{{Source: Source{ID: "lifecycle", Parser: "manifest-v1", URL: "https://example.test/lifecycle"}, Entries: []Entry{{ID: base.ID, Metadata: &core.ModelMetadata{ShutdownDate: tc.structuredDate}}}}}
			}
			next, _ := Reconcile(state, capture, observations, now, accepted)
			got := ""
			if metadata := next.Models[base.ID].Metadata; metadata != nil {
				got = metadata.ShutdownDate
			}
			if got != tc.want {
				t.Fatalf("shutdown = %q, want %q", got, tc.want)
			}
		})
	}
}
