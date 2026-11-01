package providers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	registry "github.com/Sociaro-Research/open-media-gateway/plugins/provider"
)

func operationalFixture(t *testing.T, provider, sourceID, file string) (*Manager, catalogsync.Observation, core.MediaModel) {
	t.Helper()
	m, _, _ := fixtureConnection(t)
	definition, ok := registry.Find(provider)
	if !ok {
		t.Fatal(provider)
	}
	var source catalogsync.Source
	for _, s := range definition.CatalogSources {
		if s.ID == sourceID {
			source = s
		}
	}
	body, err := os.ReadFile(filepath.Join("../../plugins/provider", provider, "testdata/video-api", file+"-parameters.txt"))
	if err != nil {
		t.Fatal(err)
	}
	_, text, err := catalogsync.Parse(source, body)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := m.InterpretCatalogSource(context.Background(), provider, source, text)
	if err != nil || len(entries) == 0 {
		t.Fatal(entries, err)
	}
	modelID := entries[0].ID
	var native core.MediaModel
	for _, model := range definition.Profiles() {
		if model.ID == modelID {
			native = model
		}
	}
	if native.ID == "" {
		t.Fatal("parsed unreviewed model", modelID)
	}
	return m, catalogsync.Observation{Source: source, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(text))), Text: text, Entries: entries}, native
}
func TestOperationalFactsPublishWithProvenanceWithoutChangingRuntimeContract(t *testing.T) {
	_, ob, model := operationalFixture(t, "alibaba", "happyhorse-r2v-api", "happyhorse")
	// GW-175 now matches the reviewed native ratios. Keep adapter-review
	// coverage with a synthetic unsupported value, not a false native mismatch.
	model.Ratios = append(append([]string(nil), model.Ratios...), "2:1")
	capture := catalogsync.Capture{Models: []core.MediaModel{model}, IDs: []string{model.ID}}
	now := time.Date(2026, 10, 26, 19, 0, 0, 0, time.UTC)
	state, run := catalogsync.Reconcile(catalogsync.State{}, capture, []catalogsync.Observation{ob}, now, nil)
	if len(run.OpenWarnings) != 1 || run.OpenWarnings[0].Reason != "Published operational field requires adapter review" {
		t.Fatal("undocumented native ratio silently accepted", run.OpenWarnings)
	}
	merged := applyCatalogModel(model, state)
	fact := merged.Metadata.Operational["output.resolutions"]
	if fact.Snapshot != ob.Hash || fact.Source.URL != ob.Source.URL || fact.CheckedAt != now.Format(time.RFC3339) || len(fact.Value) == 0 {
		t.Fatal("provider descriptor lost published provenance", merged.Metadata)
	}
	if !reflect.DeepEqual(merged.ReferenceModes, model.ReferenceModes) || !reflect.DeepEqual(merged.Ratios, model.Ratios) || !strings.Contains(strings.Join(merged.Ratios, ","), "2:1") {
		t.Fatal("evidence rewrote native compatibility")
	}
	rate := catalogsync.Rate{Source: "https://prices.example.test", Region: "Singapore", Unit: "second", Per: 1, Price: money.USD(12345)}
	entry := state.Models[model.ID]
	entry.Rates = []catalogsync.Rate{rate}
	state.Models[model.ID] = entry
	prior, _ := json.Marshal(state)
	repeat, quiet := catalogsync.Reconcile(state, capture, []catalogsync.Observation{ob}, now.Add(time.Hour), nil)
	if len(quiet.Changes) != 0 || repeat.Version != state.Version || !reflect.DeepEqual(repeat.Models[model.ID].Rates, []catalogsync.Rate{rate}) {
		t.Fatal("quiet interpretation changed confirmed data", quiet)
	}
	before := state.Models[model.ID].Metadata.Operational["output.resolutions"]
	failed := catalogsync.Observation{Source: ob.Source, Error: "Operational source interpretation failed", FetchedHash: strings.Repeat("a", 64)}
	failedState, failedRun := catalogsync.Reconcile(state, capture, []catalogsync.Observation{failed}, now.Add(24*time.Hour), nil)
	after := failedState.Models[model.ID].Metadata.Operational["output.resolutions"]
	if !reflect.DeepEqual(before, after) || failedRun.Completeness.Status != "incomplete" || !reflect.DeepEqual(failedState.Models[model.ID].Rates, []catalogsync.Rate{rate}) {
		t.Fatalf("failed source preservation: before=%+v after=%+v status=%s rates=%+v expected=%+v", before, after, failedRun.Completeness.Status, failedState.Models[model.ID].Rates, []catalogsync.Rate{rate})
	}
	unchanged, _ := json.Marshal(state)
	if string(prior) != string(unchanged) {
		t.Fatal("previous immutable state mutated")
	}
}
func TestOperationalDocumentChangesDoNotAcknowledgeFingerprintWarnings(t *testing.T) {
	_, ob, model := operationalFixture(t, "alibaba", "happyhorse-r2v-api", "happyhorse")
	// A supported subset with no disputed native selector isolates document review.
	model.Ratios = []string{"16:9", "9:16"}
	capture := catalogsync.Capture{Models: []core.MediaModel{model}, IDs: []string{model.ID}}
	now := time.Now().UTC()
	state, first := catalogsync.Reconcile(catalogsync.State{}, capture, []catalogsync.Observation{ob}, now, nil)
	if first.Completeness.Status != "complete" {
		t.Fatal("fully interpreted supported subset cannot pass", first.Completeness)
	}
	changed := ob
	changed.Hash = strings.Repeat("b", 64)
	next, run := catalogsync.Reconcile(state, capture, []catalogsync.Observation{changed}, now.Add(time.Hour), nil)
	if len(run.NewWarnings) != 1 || run.NewWarnings[0].Reason != "Documentation changed without a supported structured interpretation" || run.Completeness.Status != "incomplete" {
		t.Fatal("typed fields concealed unrelated document change", run)
	}
	_, repeat := catalogsync.Reconcile(next, capture, []catalogsync.Observation{changed}, now.Add(2*time.Hour), nil)
	if len(repeat.OpenWarnings) != 1 || len(repeat.ResolvedWarnings) != 0 {
		t.Fatal("unchanged snapshot acknowledged pending review", repeat)
	}
}
func TestOperationalMissingFieldRetainsLastConfirmedValue(t *testing.T) {
	_, ob, model := operationalFixture(t, "alibaba", "happyhorse-r2v-api", "happyhorse")
	model.Ratios = []string{"16:9"}
	capture := catalogsync.Capture{Models: []core.MediaModel{model}, IDs: []string{model.ID}}
	now := time.Now().UTC()
	state, _ := catalogsync.Reconcile(catalogsync.State{}, capture, []catalogsync.Observation{ob}, now, nil)
	// Detach input observations: production fetches also provide independent values.
	var missing catalogsync.Observation
	b, _ := json.Marshal(ob)
	json.Unmarshal(b, &missing)
	delete(missing.Entries[0].OperationalFields, "output.duration")
	missing.Hash = strings.Repeat("c", 64)
	next, run := catalogsync.Reconcile(state, capture, []catalogsync.Observation{missing}, now.Add(time.Hour), nil)
	if !reflect.DeepEqual(state.Models[model.ID].Metadata.Operational["output.duration"], next.Models[model.ID].Metadata.Operational["output.duration"]) {
		t.Fatal("missing field erased confirmed duration")
	}
	for _, m := range run.Completeness.Models {
		for _, f := range m.Fields {
			if f.Field == "output.duration" && f.Status != "stale" {
				t.Fatal("missing current field passed", f)
			}
		}
	}
}
func TestExpectedOperationalSourcesReportConfigurationGap(t *testing.T) {
	_, _, model := operationalFixture(t, "alibaba", "wan3-api", "wan3")
	definition, _ := registry.Find("alibaba")
	c := catalogsync.AssessCompleteness(catalogsync.State{}, catalogsync.Capture{Models: []core.MediaModel{model}, VerificationSources: definition.CatalogVerificationSources}, nil, nil)
	if c.Status != "incomplete" || len(c.Sources) != 1 || c.Sources[0].Status != "not_checked" || c.Sources[0].Source.ID != "wan3-api" {
		t.Fatal("absent configuration confused with a provider fetch failure", c)
	}
}
func TestDownloadedVideoOperationalSourceReplay(t *testing.T) {
	root := os.Getenv("GATEWAY_OPERATIONAL_SOURCE_FIXTURES")
	if root == "" {
		t.Skip("no exact downloaded source snapshots supplied; factual parameter fixtures run independently")
	}
	files := map[string]string{"wan3-api": "wan3.html", "wan27-r2v-api": "wan27.html", "happyhorse-r2v-api": "happyhorse.html", "video-task-api": "seedance.md"}
	for _, provider := range []string{"alibaba", "bytedance"} {
		t.Run(provider, func(t *testing.T) {
			definition, _ := registry.Find(provider)
			m, _, _ := fixtureConnection(t)
			var obs []catalogsync.Observation
			ids := map[string]bool{}
			for _, source := range definition.CatalogSources {
				file, ok := files[source.ID]
				if !ok {
					continue
				}
				body, e := os.ReadFile(filepath.Join(root, file))
				if e != nil {
					t.Fatal(e)
				}
				_, text, e := catalogsync.Parse(source, body)
				if e != nil {
					t.Fatal(e)
				}
				entries, e := m.InterpretCatalogSource(context.Background(), provider, source, text)
				if e != nil {
					t.Fatal(e)
				}
				obs = append(obs, catalogsync.Observation{Source: source, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(text))), Text: text, Entries: entries})
				for _, entry := range entries {
					ids[entry.ID] = true
				}
			}
			var models []core.MediaModel
			for _, model := range definition.Profiles() {
				if ids[model.ID] {
					models = append(models, model)
				}
			}
			c := catalogsync.AssessCompleteness(catalogsync.State{}, catalogsync.Capture{Models: models, VerificationSources: definition.CatalogVerificationSources}, obs, nil)
			for _, model := range c.Models {
				want := "complete"
				if model.Model == "happyhorse-1.1-r2v" || strings.HasPrefix(model.Model, "wan3.0-video") {
					want = "incomplete"
				}
				if model.Status != want {
					t.Fatalf("%s: %s, expected %s; fields: %+v", model.Model, model.Status, want, model.Fields)
				}
			}
		})
	}
}

type operationalRunRepository struct {
	catalogsync.Repository
	state    catalogsync.State
	run      catalogsync.Run
	schedule catalogsync.Schedule
}

func (r *operationalRunRepository) Settings(context.Context) (catalogsync.Settings, error) {
	return catalogsync.Settings{Timezone: "UTC", Version: 1}, nil
}
func (r *operationalRunRepository) Schedules(context.Context) ([]catalogsync.Schedule, error) {
	return []catalogsync.Schedule{r.schedule}, nil
}
func (r *operationalRunRepository) State(context.Context, string) (catalogsync.State, error) {
	return r.state, nil
}
func (r *operationalRunRepository) Commit(_ context.Context, _ catalogsync.Schedule, s catalogsync.State, run catalogsync.Run, _ time.Time) error {
	r.state = s
	r.run = run
	return nil
}
func (r *operationalRunRepository) WithLock(ctx context.Context, f func(context.Context) error) (bool, error) {
	return true, f(ctx)
}

type operationalRunProvider struct {
	manager *Manager
	capture catalogsync.Capture
}

func (p operationalRunProvider) CaptureCatalog(context.Context, string) (catalogsync.Capture, error) {
	return p.capture, nil
}
func (p operationalRunProvider) CheckCatalogRate(context.Context, string, core.MediaModel, catalogsync.Rate) error {
	return nil
}
func (p operationalRunProvider) InterpretCatalogSource(ctx context.Context, id string, source catalogsync.Source, text string) ([]catalogsync.Entry, error) {
	return p.manager.InterpretCatalogSource(ctx, id, source, text)
}
func TestRunnerFetchParseInterpretPublishDescriptorPath(t *testing.T) {
	manager, ob, model := operationalFixture(t, "alibaba", "happyhorse-r2v-api", "happyhorse")
	model.Ratios = []string{"16:9"}
	body, err := os.ReadFile("../../plugins/provider/alibaba/testdata/video-api/happyhorse-parameters.txt")
	if err != nil {
		t.Fatal(err)
	}
	repo := &operationalRunRepository{schedule: catalogsync.Schedule{Provider: "alibaba", Enabled: true, Requested: true, Time: "03:00", Version: 1, Sources: []catalogsync.Source{ob.Source}}}
	capture := catalogsync.Capture{Models: []core.MediaModel{model}, IDs: []string{model.ID}}
	runner := catalogsync.Runner{Repo: repo, Providers: operationalRunProvider{manager: manager, capture: capture}, Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.String() != ob.Source.URL || r.Header.Get("Authorization") != "" {
			t.Fatal("unexpected or credentialed source call", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
	})}}
	now := time.Now().UTC()
	for day := 0; day < 2; day++ {
		if err := runner.Tick(context.Background(), now.Add(time.Duration(day)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if repo.run.Status != "unchanged" || repo.run.Completeness.Status != "complete" || len(repo.run.Changes) != 0 {
		t.Fatal("runner did not consume the registered interpreter", repo.run)
	}
	merged := applyCatalogModel(model, repo.state)
	if len(merged.Metadata.Operational) == 0 || merged.Metadata.Operational["output.duration"].Source.URL != ob.Source.URL {
		t.Fatal("published descriptor lacks interpreted metadata")
	}
	prior := repo.state.Models[model.ID].Metadata.Operational["output.duration"]
	fact := merged.Metadata.Operational["output.duration"]
	fact.Value[0] = '!'
	merged.Metadata.Operational["output.duration"] = fact
	if !reflect.DeepEqual(prior, repo.state.Models[model.ID].Metadata.Operational["output.duration"]) {
		t.Fatal("descriptor alias mutated preserved catalogue evidence")
	}
}

func TestWan3KnownDurationRelationPublishesWithoutFalseWholeContractVerification(t *testing.T) {
	_, ob, model := operationalFixture(t, "alibaba", "wan3-api", "wan3")
	field := "workflow.references.video.input_output_duration"
	capture := catalogsync.Capture{Models: []core.MediaModel{model}, IDs: []string{model.ID}, VerificationSources: map[string][]catalogsync.Source{model.ID: {ob.Source}}}
	now := time.Date(2026, 10, 26, 20, 0, 0, 0, time.UTC)
	state, run := catalogsync.Reconcile(catalogsync.State{}, capture, []catalogsync.Observation{ob}, now, nil)
	fact := state.Models[model.ID].Metadata.Operational[field]
	var limit catalogsync.VideoDurationSumLimit
	if json.Unmarshal(fact.Value, &limit) != nil || limit.MaxSeconds != 30 || fact.Snapshot != ob.Hash || fact.Source.URL != ob.Source.URL {
		t.Fatal("known source relation was lost", fact)
	}
	if run.Completeness.Models[0].Status != "incomplete" {
		t.Fatal("independent native maxima falsely verified", run.Completeness)
	}
	found := false
	for _, f := range run.Completeness.Models[0].Fields {
		if f.Field == field {
			found = true
			if f.Status != "unknown" || len(f.Sources) == 0 || string(f.Sources[0].Value) != string(fact.Value) {
				t.Fatal("missing typed conflict evidence", f)
			}
		}
	}
	if !found {
		t.Fatal("required conditional rule omitted")
	}
	// A synthetic safe subset proves a path to complete verification without
	// changing any production descriptor or installing a new admission rule.
	safe := model
	safe.MaxDuration = 15
	safeCapture := capture
	safeCapture.Models = []core.MediaModel{safe}
	_, safeRun := catalogsync.Reconcile(catalogsync.State{}, safeCapture, []catalogsync.Observation{ob}, now, nil)
	if safeRun.Completeness.Models[0].Status == "incomplete" {
		t.Fatal("safe supported subset failed required verification")
	}
	for _, field := range safeRun.Completeness.Models[0].Fields {
		if field.Required && field.Status != "verified" {
			t.Fatal("required field not verified", field)
		}
	}
	unsafe := safe
	unsafe.MaxDuration = 16
	safeCapture.Models = []core.MediaModel{unsafe}
	_, unsafeRun := catalogsync.Reconcile(catalogsync.State{}, safeCapture, []catalogsync.Observation{ob}, now, nil)
	if unsafeRun.Completeness.Models[0].Status != "incomplete" {
		t.Fatal("boundary exceeded without incomplete status")
	}
	for _, failure := range []string{"omitted", "null", "failed", "unchecked"} {
		t.Run(failure, func(t *testing.T) {
			var changed catalogsync.Observation
			body, _ := json.Marshal(ob)
			if err := json.Unmarshal(body, &changed); err != nil {
				t.Fatal(err)
			}
			changed.Hash = strings.Repeat("d", 64)
			observations := []catalogsync.Observation{changed}
			switch failure {
			case "omitted":
				for i := range changed.Entries {
					delete(changed.Entries[i].OperationalFields, field)
				}
			case "null":
				for i := range changed.Entries {
					changed.Entries[i].OperationalFields[field] = json.RawMessage("null")
				}
			case "failed":
				changed.Error = "source unavailable"
				changed.Hash = ""
			case "unchecked":
				observations = nil
			}
			if failure != "unchecked" {
				observations[0] = changed
			}
			next, assessment := catalogsync.Reconcile(state, capture, observations, now.Add(time.Hour), nil)
			if !reflect.DeepEqual(fact, next.Models[model.ID].Metadata.Operational[field]) || assessment.Completeness.Models[0].Status != "incomplete" {
				t.Fatal("unavailable current rule erased prior proof or passed", failure, assessment.Completeness)
			}
		})
	}
}

func TestDownloadedVideoSourceWithoutExactModelsCannotVerify(t *testing.T) {
	root := os.Getenv("GATEWAY_OPERATIONAL_SOURCE_FIXTURES")
	if root == "" {
		t.Skip("exact downloaded source snapshots not supplied")
	}
	manager, ob, _ := operationalFixture(t, "alibaba", "wan27-r2v-api", "wan27")
	body, err := os.ReadFile(filepath.Join(root, "wan27.html"))
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := registry.Find("alibaba")
	var models []core.MediaModel
	for _, model := range definition.Profiles() {
		if model.ID == "wan2.7-r2v" || model.ID == "wan2.7-r2v-2026-06-12" {
			models = append(models, model)
		}
	}
	capture := catalogsync.Capture{Models: models, VerificationSources: definition.CatalogVerificationSources}
	for _, mutation := range []string{"original", "prefix", "suffix"} {
		t.Run(mutation, func(t *testing.T) {
			document := string(body)
			if mutation != "original" {
				pairs := []string{}
				for _, id := range []string{"wan2.7-r2v-2026-06-12", "wan2.7-r2v"} {
					value := "future-" + id
					if mutation == "suffix" {
						value = id + "-future"
					}
					pairs = append(pairs, id, value)
				}
				document = strings.NewReplacer(pairs...).Replace(document)
			}
			_, text, err := catalogsync.Parse(ob.Source, []byte(document))
			if err != nil {
				t.Fatal(err)
			}
			entries, err := manager.InterpretCatalogSource(context.Background(), "alibaba", ob.Source, text)
			observed := catalogsync.Observation{Source: ob.Source, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(text))), Entries: entries}
			if err != nil {
				observed.Error = "Operational source interpretation failed"
			}
			result := catalogsync.AssessCompleteness(catalogsync.State{}, capture, []catalogsync.Observation{observed}, nil)
			if mutation == "original" {
				if err != nil || result.Status != "complete" {
					t.Fatal("exact saved source failed positive control", err, result.Status)
				}
			} else {
				if err == nil || len(entries) != 0 || result.Status != "incomplete" {
					t.Fatal("absent exact models verified", mutation, err, result.Status)
				}
				for _, model := range result.Models {
					if model.Status != "incomplete" {
						t.Fatal("missing exact model passed", model.Model)
					}
				}
			}
		})
	}
}
