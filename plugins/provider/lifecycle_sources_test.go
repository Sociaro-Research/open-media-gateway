package providerassets

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// Synthetic notices deliberately mix lifecycle phases and media/text IDs. A
// source declaration must retain evidence without inventing an executable model
// or flattening new-user restrictions, region and postponement into shutdown.
var lifecycleDocuments = []struct{ provider, id, url, notice string }{
	{"agnes", "documentation-index", "https://wiki.agnes-ai.com/llms.txt", "Current media models: reviewed-media, unknown-future-media. Text models: unrelated-text. Index membership is not an availability date."},
	{"agnes", "video-2.0-retirement", "https://wiki.agnes-ai.com/en/docs/agnes-video-v20.md", "Historical media retirement: legacy-media ends 2030-06-01 23:59:59 UTC+8. This notice does not describe unknown-future-media."},
	{"alibaba", "model-list", "https://www.alibabacloud.com/help/en/model-studio/model-studio-model-list/", "Image and video models: reviewed-media, unknown-future-media. Text models: unrelated-text. Region-specific availability remains unknown."},
	{"alibaba", "image-model-list", "https://www.alibabacloud.com/help/en/model-studio/model-list-image-generation", "Image models: reviewed-media, unknown-future-media. Listed IDs do not establish reviewed contracts or account availability."},
	{"alibaba", "video-model-list", "https://www.alibabacloud.com/help/en/model-studio/model-list-video-generation", "Video models: reviewed-media, unknown-future-media. A newly listed ID remains unreviewed."},
	{"alibaba", "speech-model-list", "https://www.alibabacloud.com/help/en/model-studio/model-list-speech-synthesis", "Speech models and voice-enrollment helpers are listed together. Only reviewed media contracts may execute."},
	{"alibaba", "music-models", "https://www.alibabacloud.com/help/en/model-studio/fun-music", "Music models: reviewed-media and unknown-future-media. Limited preview; Beijing region only. Listing is not account entitlement."},
	{"alibaba", "lifecycle-policy", "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/model-depreciation.md", "No new users: 2030-01-01. EoM: 2030-02-01. EoS: TBD. Region: Singapore only. Previous shutdown plan postponed; do not use the old date."},
	{"bytedance", "model-deprecations", "https://docs.byteplus.com/en/docs/ModelArk/model-deprecation-notice", "Notification: 2030-01-01. Deprecation: 2030-02-01. Deactivation: 2030-03-01 17:00 UTC+8. Region: eu-west-1 only; other regions unknown."},
	{"minimax", "guides/models-intro", "https://platform.minimax.io/docs/guides/models-intro.md", "Media: reviewed-media and unknown-future-media. Text: unrelated-text. Dates not specified."},
	{"minimax", "release-notes/models", "https://platform.minimax.io/docs/release-notes/models.md", "2030-01-01: unrelated-text retires. Music: no new users; existing paid users continue. This is not a video-model shutdown."},
	{"minimax", "release-notes/apis", "https://platform.minimax.io/docs/release-notes/apis.md", "2030-02-01: new optional API parameter. Media service start and retirement remain unspecified."},
	{"openai", "models-index", "https://developers.openai.com/api/docs/models/all.md", "Current and deprecated models: reviewed-media, unknown-future-media, unrelated-text. Deprecated is not a shutdown date."},
	{"openai", "changelog", "https://developers.openai.com/api/docs/changelog", "2030-01-01: announced unknown-future-media. Announcement does not establish this account's availability."},
	{"openai", "deprecations", "https://developers.openai.com/api/docs/deprecations", "Deprecation announced 2030-01-01; shutdown 2030-09-01. Keep these distinct. unrelated-text is outside media scope."},
	{"kling", "doc-updates-api", "https://kling.ai/document-api/updates/api.md", "2030-02-01: unknown-future-media released. API entitlement and reviewed adapter support are independent."},
	{"sonilo", "api-changelog", "https://sonilo.com/docs/changelog", "2030-02-01: operation default changed. New API operation documented; no vendor-model release or retirement dates supplied."},
}

type lifecycleTransport func(*http.Request) (*http.Response, error)

func (f lifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLifecycleSourcesRetainEvidenceWithoutInferringModels(t *testing.T) {
	for _, tc := range lifecycleDocuments {
		t.Run(tc.provider+"/"+tc.id, func(t *testing.T) {
			d, ok := Find(tc.provider)
			if !ok {
				t.Fatal("provider missing")
			}
			var source catalogsync.Source
			for _, candidate := range d.CatalogSources {
				if candidate.ID == tc.id {
					source = candidate
				}
			}
			if source.URL != tc.url || source.Parser != "document" {
				t.Fatalf("missing authoritative document source: %+v", source)
			}
			if err := (catalogsync.Schedule{Provider: d.ID, Version: 1, Time: "03:00", Sources: d.CatalogSources}).Validate(); err != nil {
				t.Fatal(err)
			}
			calls := 0
			client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.String() != tc.url || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Fatal("unexpected public-source request", r.Method, r.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.notice)), Header: http.Header{}}, nil
			})}
			ob := catalogsync.Fetch(context.Background(), client, source)
			if calls != 1 || ob.Error != "" || ob.Hash == "" || len(ob.Entries) != 0 || ob.Text != tc.notice {
				t.Fatalf("lost or inferred lifecycle evidence: %+v", ob)
			}
			if d.CatalogInterpret != nil {
				entries, err := d.CatalogInterpret(source, ob.Text)
				if err != nil || len(entries) != 0 {
					t.Fatalf("non-price notice interpreted as executable data: %+v, %v", entries, err)
				}
			}
			// Preserve a confirmed tariff and contract when a document changes,
			// fails or recovers. No document-only new ID may be admitted.
			now := time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)
			capture := catalogsync.Capture{Models: []core.MediaModel{{ID: "reviewed-media", MediaType: "image"}}, DocumentReviews: d.CatalogReviews}
			accept := func(core.MediaModel, catalogsync.Rate) error { return nil }
			state, _ := catalogsync.Reconcile(catalogsync.State{}, capture, []catalogsync.Observation{ob}, now, accept)
			entry := state.Models["reviewed-media"]
			entry.Rates = []catalogsync.Rate{{Region: "confirmed-region", Unit: "image", Per: 1, Price: 100000}}
			state.Models[entry.ID] = entry
			version := state.Version
			changed := ob
			changed.Hash += "-changed"
			changed.Text += " Updated: unknown-future-media; contradictory date remains unresolved."
			for i, observation := range []catalogsync.Observation{changed, changed, {Source: source, Error: "Source returned HTTP 503"}, changed} {
				next, run := catalogsync.Reconcile(state, capture, []catalogsync.Observation{observation}, now.Add(time.Duration(i+1)*time.Hour), accept)
				if len(run.OpenWarnings) == 0 || next.Version != version || len(run.Changes) != 0 || len(next.Models) != 1 || !reflect.DeepEqual(next.Models[entry.ID], entry) {
					t.Fatalf("document changed executable catalogue or lost warning: %+v", run)
				}
				if next.Observations[source.ID].Review != nil {
					t.Fatal("source addition silently acknowledged a document")
				}
				state = next
			}
		})
	}
}

func TestBytePlusNoticeIdentitiesRemainIndependent(t *testing.T) {
	d, _ := Find("bytedance")
	want := map[string]string{
		"deprecations":       "https://ai.byteplus.com/ark/region:ap-southeast-1/docs/model-deprecation-notice",
		"model-deprecations": "https://docs.byteplus.com/en/docs/ModelArk/model-deprecation-notice",
	}
	var observations []catalogsync.Observation
	client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("Same published notice body; source identities and their review fingerprints remain distinct even when the current content matches.")), Header: http.Header{}}, nil
	})}
	for _, source := range d.CatalogSources {
		if url, ok := want[source.ID]; ok {
			if source.URL != url || source.Parser != "document" {
				t.Fatalf("source identity changed: %+v", source)
			}
			observations = append(observations, catalogsync.Fetch(context.Background(), client, source))
		}
	}
	if len(observations) != 2 || observations[0].Hash != observations[1].Hash || observations[0].Hash == "" {
		t.Fatal("fixture must exercise distinct sources with matching content")
	}
	state, _ := catalogsync.Reconcile(catalogsync.State{}, catalogsync.Capture{}, observations, time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), nil)
	for id, url := range want {
		if ob, ok := state.Observations[id]; !ok || ob.Source.URL != url {
			t.Fatalf("matching hash deduplicated source %s", id)
		}
	}
	regional := want["deprecations"]
	if d.CatalogSourceAliases[regional] != regional+".md" || d.CatalogSourceAliases[want["model-deprecations"]] != "" {
		t.Fatal("separate general source inherited the regional Markdown alias")
	}
	if !reflect.DeepEqual(d.CatalogLifecycleSources, []catalogsync.Source{{ID: "deprecations", URL: regional + ".md", Parser: "document"}}) {
		t.Fatal("upstream reviewed lifecycle source changed")
	}
}
