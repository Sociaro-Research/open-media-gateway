package providers

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

func TestLatestDocumentReviewsThroughConfiguredConnections(t *testing.T) {
	for _, tc := range []struct {
		provider  string
		ids       []string
		url, hash string
		resolved  int
	}{
		{"agnes", []string{"agnes-image-2.5-flash", "agnes-2.5-flash", "agnes-2.5-pro-alpha", "agnes-2.5-pro-beta", "agnes-3.0-flash"}, "https://www.agnes-ai.com/en/docs/pricing", "05436567053a8da5cfcb0a389e0b3fc78487d318fe7c275267d59908792c1a23", 5},
		{"openai", []string{"gpt-image-2.5-flare"}, "https://developers.openai.com/api/docs/deprecations", "883ba9d155f008ae89e928a137b24e4ce960250a67e2b12ddab1e4656ae197e1", 1},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			m, _, ctx := fixtureConnection(t)
			ctx = identity.WithResourceOwners(identity.WithOwnerID(ctx, identity.SharedOwnerID), identity.SharedOwnerID, identity.SharedOwnerID)
			var ids []map[string]string
			for _, id := range tc.ids {
				ids = append(ids, map[string]string{"id": id})
			}
			body, _ := json.Marshal(map[string]any{"data": ids})
			m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/models") {
					t.Fatal("unexpected request", r.Method, r.URL)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
			})
			if _, err := m.Update(ctx, tc.provider, ProviderUpdate{APIKey: pointer("fixture"), Enabled: pointer(true), Connection: &providerkit.Connection{}}); err != nil {
				t.Fatal(err)
			}
			capture, err := m.CaptureCatalog(ctx, tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			source := catalogsync.Source{ID: "document", URL: tc.url, Parser: "document"}
			obs := []catalogsync.Observation{{Source: source, Hash: tc.hash}}
			previous := catalogsync.State{Observations: map[string]catalogsync.Observation{source.ID: {Source: source, Hash: "previous"}}}
			// Recreate the production warnings with the pre-review behaviour.
			unreviewed := capture
			unreviewed.Excluded, unreviewed.DocumentReviews = nil, nil
			previous, old := catalogsync.Reconcile(previous, unreviewed, obs, time.Now(), func(core.MediaModel, catalogsync.Rate) error { return nil })
			if len(old.OpenWarnings) != tc.resolved {
				t.Fatal("original warning count", len(old.OpenWarnings))
			}
			next, run := catalogsync.Reconcile(previous, capture, obs, time.Now(), func(core.MediaModel, catalogsync.Rate) error { return nil })
			if len(run.OpenWarnings) != 0 || len(run.ResolvedWarnings) != tc.resolved || run.Sources[0].Review == nil {
				t.Fatal("configured review lost", run)
			}
			for _, excluded := range capture.Excluded {
				if next.Models[excluded.ID].Contract != nil || next.Models[excluded.ID].ExcludedReason == "" {
					t.Fatal("text model enabled or scope evidence lost", excluded.ID)
				}
			}
			_, repeat := catalogsync.Reconcile(next, capture, obs, time.Now(), func(core.MediaModel, catalogsync.Rate) error { return nil })
			if len(repeat.Changes) != 0 || len(repeat.NewWarnings) != 0 || repeat.EmailStatus != "not_needed" {
				t.Fatal("repeat not quiet", repeat)
			}
			capture.IDs = append(capture.IDs, "future-unknown-model")
			_, unknown := catalogsync.Reconcile(next, capture, obs, time.Now(), func(core.MediaModel, catalogsync.Rate) error { return nil })
			if len(unknown.OpenWarnings) != 1 || unknown.OpenWarnings[0].Model != "future-unknown-model" {
				t.Fatal("unknown model bypassed review", unknown)
			}
			adapter, err := m.resolve(ctx, tc.provider, true)
			if err != nil {
				t.Fatal(err)
			}
			wrapped, ok := adapter.(*connectionAdapter)
			if !ok {
				t.Fatal("not a configured connection")
			}
			wrapped.credentials.Connection.ManualCatalog = true
			if len(wrapped.CatalogExclusions()) != 0 {
				t.Fatal("manual catalogue received native exclusions")
			}
		})
	}
}

// Opt-in full replay from sanitized, read-only production catalogue evidence.
// All HTTP requests use local response fixtures; no provider generation occurs.
func TestLatestProductionCatalogueReplay(t *testing.T) {
	file := os.Getenv("GATEWAY_CATALOG_REVIEW_SNAPSHOT")
	if file == "" {
		t.Skip("no read-only production catalogue evidence")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		States []struct {
			Provider string
			Payload  catalogsync.State
		}
		Documents []struct{ Hash, Content string }
		API       []struct{ Hash, Content string }
	}
	if err = json.Unmarshal(data, &evidence); err != nil {
		t.Fatal(err)
	}
	documents := map[string]string{}
	for _, d := range append(evidence.Documents, evidence.API...) {
		if fmt.Sprintf("%x", sha256.Sum256([]byte(d.Content))) != d.Hash {
			t.Fatal("evidence hash mismatch", d.Hash)
		}
		documents[d.Hash] = d.Content
	}
	for _, snapshot := range evidence.States {
		if snapshot.Provider == "bytedance" {
			continue
		}
		t.Run(snapshot.Provider, func(t *testing.T) {
			previous := snapshot.Payload
			m, _, ctx := fixtureConnection(t)
			ctx = identity.WithResourceOwners(identity.WithOwnerID(ctx, identity.SharedOwnerID), identity.SharedOwnerID, identity.SharedOwnerID)
			var ids []string
			if err := json.Unmarshal([]byte(documents[previous.Observations["_api"].Hash]), &ids); err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			for _, id := range ids {
				row := map[string]any{"id": id}
				if snapshot.Provider == "alibaba" {
					row = map[string]any{"model": id}
					facts := map[string]string{}
					if md := previous.Models[id].Metadata; md != nil {
						for _, f := range md.Facts {
							facts[f.Label] = f.Value
						}
					}
					// API facts come from the retained production metadata. IDs
					// without reviewed metadata still participate in discovery.
					row["capabilities"] = strings.Split(facts["API capabilities"], ", ")
					row["inference_metadata"] = map[string]any{"request_modality": strings.Split(facts["API input modalities"], ", "), "response_modality": strings.Split(facts["API output modalities"], ", ")}
					if id == "qwen-audio-3.1-tts-flash" {
						row["capabilities"] = []string{"TTS"}
					}
				}
				rows = append(rows, row)
			}
			response := map[string]any{"data": rows}
			if snapshot.Provider == "alibaba" {
				response = map[string]any{"output": map[string]any{"models": rows, "total": len(rows)}}
			}
			body, _ := json.Marshal(response)
			m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/models") {
					t.Fatal("unexpected request", r.Method, r.URL)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
			})
			if _, err := m.Update(ctx, snapshot.Provider, ProviderUpdate{APIKey: pointer("fixture"), Enabled: pointer(true), Connection: &providerkit.Connection{}}); err != nil {
				t.Fatal(err)
			}
			capture, err := m.CaptureCatalog(ctx, snapshot.Provider)
			if err != nil {
				t.Fatal(err)
			}
			var obs []catalogsync.Observation
			var sourceIDs []string
			for id := range previous.Observations {
				sourceIDs = append(sourceIDs, id)
			}
			sort.Strings(sourceIDs)
			for _, id := range sourceIDs {
				ob := previous.Observations[id]
				if review, ok := capture.DocumentReviews[ob.Source.URL]; ok && documents[review.Hash] == "" {
					t.Fatal("missing reviewed document")
				}
				obs = append(obs, ob)
			}
			check := func(model core.MediaModel, rate catalogsync.Rate) error {
				return m.CheckCatalogRate(ctx, snapshot.Provider, model, rate)
			}
			next, run := catalogsync.Reconcile(previous, capture, obs, time.Now(), check)
			want := map[string]int{"agnes": 0, "openai": 0, "alibaba": 3}[snapshot.Provider]
			if len(run.OpenWarnings) != want {
				t.Fatalf("open %d, expected %d: %+v", len(run.OpenWarnings), want, run.OpenWarnings)
			}
			t.Logf("resolved=%d open=%d changes=%d", len(run.ResolvedWarnings), len(run.OpenWarnings), len(run.Changes))
			liveModels := capture.Models
			for i := 0; i < 3; i++ {
				capture.Models = liveModels
				if snapshot.Provider == "alibaba" && i%2 == 0 {
					// Alternate to the reviewed offline descriptors, matching
					// the metadata removals in the preceding production report.
					capture.Models = nil
				}
				var repeat catalogsync.Run
				next, repeat = catalogsync.Reconcile(next, capture, obs, time.Now(), check)
				if len(repeat.Changes) != 0 || len(repeat.NewWarnings) != 0 || len(repeat.OpenWarnings) != want || repeat.EmailStatus != "not_needed" {
					t.Fatalf("repeat %d not quiet: changes=%+v warnings=%+v", i, repeat.Changes, repeat.OpenWarnings)
				}
			}
		})
	}
}
