package catalogsync

import (
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

func TestReviewedScopeRemainsQuietAndCanBecomeSupported(t *testing.T) {
	now := time.Now()
	capture := Capture{IDs: []string{"session"}, Excluded: []Exclusion{{ID: "session", Reason: "Interactive session outside file generation", Metadata: &core.ModelMetadata{Sources: []core.ModelSource{{URL: "https://example.test/contract"}}}}}}
	state, first := Reconcile(State{}, capture, []Observation{{Source: Source{ID: "_api"}}}, now, accepted)
	if len(first.Changes) == 0 || len(first.OpenWarnings) != 0 || state.Models["session"].Contract != nil {
		t.Fatal("reviewed scope decision not recorded")
	}
	_, repeat := Reconcile(state, capture, []Observation{{Source: Source{ID: "_api"}}}, now, accepted)
	if len(repeat.Changes) != 0 || len(repeat.OpenWarnings) != 0 {
		t.Fatal("scope decision repeats as a daily change")
	}
	capture.Excluded = nil
	capture.Models = []core.MediaModel{{ID: "session", MediaType: "video"}}
	updated, _ := Reconcile(state, capture, []Observation{{Source: Source{ID: "_api"}}}, now, accepted)
	if updated.Models["session"].ExcludedReason != "" || updated.Models["session"].Contract == nil {
		t.Fatal("code review could not replace an earlier exclusion")
	}
}

func TestReviewedRegionalModelAndDisappearanceAreDifferent(t *testing.T) {
	now := time.Now()
	m := core.MediaModel{ID: "regional", MediaType: "image"}
	capture := Capture{Reviewed: []core.MediaModel{m}}
	obs := []Observation{{Source: Source{ID: "_api"}}}
	state, run := Reconcile(State{}, capture, obs, now, accepted)
	if len(run.OpenWarnings) != 0 || state.Models[m.ID].DiscoveryStatus != "not_discovered" {
		t.Fatal("a reviewed regional model falsely requires an adapter")
	}
	capture.IDs = []string{m.ID}
	state, _ = Reconcile(state, capture, obs, now, accepted)
	capture.IDs = nil
	state, run = Reconcile(state, capture, obs, now, accepted)
	if len(run.OpenWarnings) != 1 || state.Models[m.ID].DiscoveryStatus != "missing" {
		t.Fatal("unexpected disappearance was not reported")
	}
	_, run = Reconcile(state, capture, obs, now, accepted)
	if len(run.OpenWarnings) != 1 {
		t.Fatal("disappearance warning resolved without evidence")
	}
	capture.IDs = []string{m.ID}
	_, run = Reconcile(state, capture, obs, now, accepted)
	if len(run.OpenWarnings) != 0 || len(run.ResolvedWarnings) != 1 {
		t.Fatal("rediscovery did not resolve disappearance warning")
	}
}
