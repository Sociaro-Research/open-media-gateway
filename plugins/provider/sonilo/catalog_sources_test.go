package sonilo

import (
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestCompleteSpecificationPreservesExistingSources(t *testing.T) {
	d := Definition()
	want := map[string]catalogsync.Source{
		"api":              {ID: "api", URL: "https://platform.sonilo.com/docs/api", Parser: "document"},
		"openapi":          {ID: "openapi", URL: "https://sonilo.com/openapi.json", Parser: "document"},
		"openapi-complete": {ID: "openapi-complete", URL: "https://platform.sonilo.com/openapi.json", Parser: "document"},
		"pricing":          {ID: "pricing", URL: "https://platform.sonilo.com/pricing", Parser: "document"},
	}
	for _, source := range d.CatalogSources {
		if expected, ok := want[source.ID]; !ok || source != expected {
			t.Fatalf("unexpected or duplicate source: %+v", source)
		}
		delete(want, source.ID)
	}
	if len(want) != 0 {
		t.Fatalf("lost independent source evidence: %+v", want)
	}
	for _, model := range d.Profiles() {
		sources := d.CatalogVerificationSources[model.ID]
		if len(sources) != 1 || sources[0] != completeOpenAPISource() {
			t.Fatalf("native operation %s lost its complete-spec requirement: %+v", model.ID, sources)
		}
	}
}

func TestCompleteSpecificationAdoptionRetainsUnverifiedFieldsAndWarnings(t *testing.T) {
	d := Definition()
	// These native paths are entirely absent from the public core subset.
	var models []core.MediaModel
	for _, id := range []string{"video-to-video-music", "video-to-video-sfx", "video-to-sound", "video-to-video-sound"} {
		models = append(models, modelFor(id))
	}
	capture := catalogsync.Capture{Models: models, VerificationSources: d.CatalogVerificationSources}
	core := catalogsync.Observation{
		Source: catalogsync.Source{ID: "openapi", URL: "https://sonilo.com/openapi.json", Parser: "document"},
		Hash:   strings.Repeat("a", 64),
	}
	now := time.Date(2026, 11, 5, 13, 0, 0, 0, time.UTC)
	state, _ := catalogsync.Reconcile(catalogsync.State{}, capture, []catalogsync.Observation{core}, now, nil)
	core.Hash = strings.Repeat("b", 64)
	state, changed := catalogsync.Reconcile(state, capture, []catalogsync.Observation{core}, now.Add(time.Minute), nil)
	if len(changed.OpenWarnings) != 1 {
		t.Fatalf("core document edit did not retain its warning: %+v", changed.OpenWarnings)
	}
	warningKey := changed.OpenWarnings[0].Key

	for _, tc := range []struct {
		name, status string
		complete     *catalogsync.Observation
	}{
		{name: "existing core-only schedule", status: "not_checked"},
		{name: "failed complete spec", status: "retrieval_failed", complete: &catalogsync.Observation{
			Source: completeOpenAPISource(), Error: "Source returned HTTP 503",
		}},
		// A deliberate administrator source ID must not prevent URL matching.
		{name: "adopted under custom ID", status: "retrieved", complete: &catalogsync.Observation{
			Source: catalogsync.Source{ID: "admin-complete-spec", URL: "https://platform.sonilo.com/openapi.json", Parser: "document"},
			Hash:   strings.Repeat("c", 64),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observations := []catalogsync.Observation{core}
			if tc.complete != nil {
				observations = append(observations, *tc.complete)
			}
			next, run := catalogsync.Reconcile(state, capture, observations, now.Add(2*time.Minute), nil)
			if next.Version != state.Version || len(run.Changes) != 0 || len(run.ResolvedWarnings) != 0 {
				t.Fatal("source adoption changed publication or acknowledged a warning", run)
			}
			retained := false
			for _, warning := range run.OpenWarnings {
				retained = retained || warning.Key == warningKey
			}
			if !retained {
				t.Fatal("original core document warning was lost")
			}
			coverage := run.Completeness
			if coverage == nil || coverage.Status != "incomplete" || len(coverage.Models) != len(capture.Models) {
				t.Fatal("uninterpreted documents claimed operational verification", coverage)
			}
			found := false
			for _, source := range coverage.Sources {
				if source.Source.URL == completeOpenAPISource().URL {
					found = true
					if source.Status != tc.status {
						t.Fatalf("complete specification status = %q, want %q", source.Status, tc.status)
					}
				}
			}
			if !found {
				t.Fatal("complete specification absent from coverage")
			}
			for _, model := range coverage.Models {
				if len(d.CatalogVerificationSources[model.Model]) == 0 || model.Status != "incomplete" {
					t.Fatalf("native operation lost its required source or claimed verification: %+v", model)
				}
				for _, field := range model.Fields {
					if field.Status == "verified" {
						t.Fatalf("document-only observation verified %s/%s", model.Model, field.Field)
					}
				}
			}
		})
	}
}
