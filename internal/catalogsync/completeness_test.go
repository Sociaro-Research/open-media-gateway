package catalogsync

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func coverageField(t *testing.T, c *Completeness, model, field string) FieldCoverage {
	t.Helper()
	for _, m := range c.Models {
		if m.Model == model {
			for _, f := range m.Fields {
				if f.Field == field {
					return f
				}
			}
		}
	}
	t.Fatalf("missing coverage %s/%s", model, field)
	return FieldCoverage{}
}
func completeImageModel() core.MediaModel {
	return core.MediaModel{ID: "image", MediaType: "image", Resolutions: []string{"1024x1024"}, Ratios: []string{"1:1"}}
}
func structuredContract(m core.MediaModel) Observation {
	ob := Observation{Source: Source{ID: "operational-contract", URL: "https://example.test/contracts", Parser: "manifest-v1"}, Entries: []Entry{{ID: m.ID, Contract: &m}}}
	ob.Text = string(encoded(ob.Entries))
	ob.Hash = fmt.Sprintf("%x", sha256.Sum256([]byte(ob.Text)))
	return ob
}
func TestCompletenessOptionalFileMetadataDoesNotFailRequiredContract(t *testing.T) {
	now := time.Now().UTC()
	m := completeImageModel()
	m.SupportsImageInput = true
	m.ReferenceModes = []core.ReferenceMode{{ID: "references", MinImages: 1, MaxImages: 9}}
	capture := Capture{Models: []core.MediaModel{m}, IDs: []string{m.ID}}
	ob := structuredContract(m)
	state, _ := Reconcile(State{}, capture, []Observation{ob}, now, accepted)
	before := string(encoded(state))
	next, run := Reconcile(state, capture, []Observation{ob}, now.Add(24*time.Hour), accepted)
	if run.Status != "partial" || run.Completeness.Status != "partial" || len(run.Changes) != 0 || next.Version != state.Version || len(run.OpenWarnings) != 0 {
		t.Fatalf("optional limits misclassified as required or hidden: %+v", run)
	}
	f := coverageField(t, run.Completeness, m.ID, "reference_media_limits")
	if f.Status != "unknown" || f.Required || !strings.Contains(f.Reason, "Optional") {
		t.Fatal(f)
	}
	if f := coverageField(t, run.Completeness, m.ID, "workflow.references.video"); f.Status != "unsupported" {
		t.Fatal(f)
	}
	if string(encoded(state)) != before {
		t.Fatal("assessment mutated previous evidence")
	}
	_, text, markup := Report(run)
	for _, part := range []string{"OPERATIONAL COMPLETENESS: partial", "reference_media_limits", "Current adapter contract", "snapshot " + ob.Hash} {
		if !strings.Contains(text, part) || !strings.Contains(markup, part) {
			t.Fatalf("report omitted %s", part)
		}
	}
}
func TestCompletenessRequiresExactStructuredOperationalEvidence(t *testing.T) {
	m := completeImageModel()
	ob := structuredContract(m)
	for _, test := range []struct {
		name   string
		modify func(*Observation)
		want   string
	}{
		{"exact", func(*Observation) {}, "complete"},
		{"prose", func(o *Observation) { o.Source.Parser = "document" }, "incomplete"},
		{"generic capability facts", func(o *Observation) {
			o.Entries = []Entry{{ID: m.ID, Metadata: &core.ModelMetadata{Facts: []core.ModelFact{{Label: "Provider: Resolution", Value: "1024x1024"}}}}}
		}, "incomplete"},
		{"missing snapshot", func(o *Observation) { o.Hash = "" }, "incomplete"},
		{"invalid snapshot", func(o *Observation) { o.Hash = strings.Repeat("z", 64) }, "incomplete"},
		{"different contract", func(o *Observation) {
			other := m
			other.Ratios = []string{"16:9"}
			o.Entries = []Entry{{ID: m.ID, Contract: &other}}
		}, "incomplete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := ob
			test.modify(&copy)
			c := AssessCompleteness(State{}, Capture{Models: []core.MediaModel{m}}, []Observation{copy}, nil)
			if c.Status != test.want {
				t.Fatal(c)
			}
		})
	}
	c := AssessCompleteness(State{}, Capture{Models: []core.MediaModel{m}}, []Observation{ob}, nil)
	if f := coverageField(t, c, m.ID, "media_type"); f.Status != "verified" || len(f.Sources) != 1 || f.Sources[0].Hash != ob.Hash {
		t.Fatal(f)
	}
	// Text-to-image does not require reference file metadata or video duration.
	for _, field := range []string{"references", "output.duration", "voices", "output.formats"} {
		if f := coverageField(t, c, m.ID, field); f.Status != "not_applicable" {
			t.Fatal(f)
		}
	}
	_, legacy, _ := Report(Run{})
	if !strings.Contains(legacy, "not assessed (historical run)") {
		t.Fatal(legacy)
	}
}
func TestCompletenessPreservesFailureAndStaleEvidence(t *testing.T) {
	m := completeImageModel()
	ob := structuredContract(m)
	previous := State{Observations: map[string]Observation{ob.Source.ID: ob}}
	for _, test := range []struct {
		name         string
		obs          []Observation
		want         string
		sourceStatus string
	}{
		{"failed retrieval", []Observation{{Source: ob.Source, Error: "Source returned HTTP 503", FetchedHash: strings.Repeat("b", 64)}}, "preserved", "retrieval_failed"},
		{"changed uninterpreted source", []Observation{{Source: ob.Source, Hash: strings.Repeat("b", 64)}}, "stale", "retrieved"},
		{"removed source", nil, "stale", "stale"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := AssessCompleteness(previous, Capture{Models: []core.MediaModel{m}}, test.obs, nil)
			f := coverageField(t, c, m.ID, "media_type")
			if c.Status != "incomplete" || f.Status != test.want || f.Sources[0].LastConfirmedHash != ob.Hash || c.Sources[0].Status != test.sourceStatus {
				t.Fatalf("%+v %+v", c, f)
			}
		})
	}
}
func TestCompletenessScopeAndModalRequirements(t *testing.T) {
	image := completeImageModel()
	speech := core.MediaModel{ID: "speech", MediaType: "speech", Formats: []string{"mp3"}, SupportsVoice: true, Voices: []string{"voice"}}
	video := core.MediaModel{ID: "video", MediaType: "video", Resolutions: []string{"720p"}, Ratios: []string{"16:9"}, DurationFromInput: true}
	retired := image
	retired.ID = "retired"
	excluded := image
	excluded.ID = "excluded"
	c := AssessCompleteness(State{}, Capture{Models: []core.MediaModel{video, image, speech, retired, excluded}, Inactive: []core.MediaModel{retired}, Excluded: []Exclusion{{ID: excluded.ID}}}, []Observation{structuredContract(image), structuredContract(speech), structuredContract(video)}, nil)
	if c.Status != "complete" || len(c.Models) != 3 {
		t.Fatal(c)
	}
	if coverageField(t, c, speech.ID, "output.resolutions").Status != "not_applicable" || coverageField(t, c, speech.ID, "voices").Status != "verified" || coverageField(t, c, video.ID, "output.duration").Status != "not_applicable" {
		t.Fatal(c)
	}
	video.DurationFromInput = false
	c = AssessCompleteness(State{}, Capture{Models: []core.MediaModel{video}}, []Observation{structuredContract(video)}, nil)
	if coverageField(t, c, video.ID, "output.duration").Status != "unknown" {
		t.Fatal(c)
	}
}
func TestCompletenessDoesNotTouchTariffsOrWarningHistory(t *testing.T) {
	m := completeImageModel()
	ob := structuredContract(m)
	now := time.Now().UTC()
	previous, _ := Reconcile(State{}, Capture{Models: []core.MediaModel{m}}, []Observation{ob}, now, accepted)
	entry := previous.Models[m.ID]
	entry.Rates = []Rate{{Region: "Singapore", Unit: "image", Per: 1, Price: 123}}
	previous.Models[m.ID] = entry
	warning := Warning{Key: "pending", Source: "other", Reason: "Documentation changed", Evidence: encoded(map[string]string{"hash": "preserved"}), FirstSeen: now, LastSeen: now, Occurrences: 2}
	previous.Warnings = []Warning{warning}
	next, run := Reconcile(previous, Capture{Models: []core.MediaModel{m}}, []Observation{ob}, now.Add(time.Hour), accepted)
	if !reflect.DeepEqual(next.Models[m.ID].Rates, entry.Rates) || len(run.OpenWarnings) != 1 || string(run.OpenWarnings[0].Evidence) != string(warning.Evidence) || run.Completeness.Status != "incomplete" {
		t.Fatal("lost tariff or pending evidence", run)
	}
}
func TestRunnerReportsUnchangedIncompleteEveryDay(t *testing.T) {
	repo := &inactiveRunnerRepository{}
	provider := &coverageRunnerProvider{model: completeImageModel()}
	runner := Runner{Repo: repo, Providers: provider}
	for day := 0; day < 2; day++ {
		if err := runner.check(context.Background(), Schedule{Provider: "fixture", Time: "03:00"}, Settings{Timezone: "UTC", Email: "report@example.test"}, time.Now().Add(time.Duration(day)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if repo.run.Status != "incomplete" || repo.run.EmailStatus != "pending" || len(repo.run.Changes) != 0 {
		t.Fatal(repo.run)
	}
	if err := runner.check(context.Background(), Schedule{Provider: "fixture", Time: "03:00"}, Settings{Timezone: "UTC"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if repo.run.EmailStatus != "disabled" {
		t.Fatal(repo.run)
	}
}

type coverageRunnerProvider struct{ model core.MediaModel }

func (p *coverageRunnerProvider) CaptureCatalog(context.Context, string) (Capture, error) {
	return Capture{Models: []core.MediaModel{p.model}, IDs: []string{p.model.ID}}, nil
}
func (p *coverageRunnerProvider) CheckCatalogRate(context.Context, string, core.MediaModel, Rate) error {
	return nil
}

func TestCompletenessIgnoresWarningsOutsideSupportedScope(t *testing.T) {
	m := completeImageModel()
	c := AssessCompleteness(State{}, Capture{Models: []core.MediaModel{m}}, []Observation{structuredContract(m)}, []Warning{{Model: "future-unsupported", Reason: "Model requires an adapter contract"}, {Model: "retired", Reason: "Historical tariff"}})
	if c.Status != "complete" {
		t.Fatal("unsupported/retired records expanded connected-model coverage", c)
	}
}
func TestCompletenessAudioAndAbsentSelectors(t *testing.T) {
	for _, m := range []core.MediaModel{{ID: "fixed-image", MediaType: "image"}, {ID: "audio", MediaType: "audio", DurationFromInput: true, Formats: []string{"wav"}}} {
		c := AssessCompleteness(State{}, Capture{Models: []core.MediaModel{m}}, []Observation{structuredContract(m)}, nil)
		if c.Status != "complete" {
			t.Fatal("inapplicable selectors required", c)
		}
	}
}

func TestCompletenessKeepsOriginalSourceIdentityAndDetachesContracts(t *testing.T) {
	m := completeImageModel()
	old := structuredContract(m)
	failed := Observation{Source: old.Source, Error: "Source request failed"}
	failed.Source.URL = "https://replacement.example.test/contracts"
	c := AssessCompleteness(State{Observations: map[string]Observation{old.Source.ID: old}}, Capture{Models: []core.MediaModel{m}}, []Observation{failed}, nil)
	f := coverageField(t, c, m.ID, "media_type")
	if f.Status != "preserved" || f.Sources[0].Source.URL != old.Source.URL || f.Sources[0].LastConfirmedHash != old.Hash || c.Sources[0].LastConfirmedHash != "" {
		t.Fatal("misattributed retained source", c)
	}
	if _, ok := c.Models[0].Contract["pricing"]; ok {
		t.Fatal("omitted tariff represented as a fabricated zero price")
	}
	m.Resolutions[0] = "mutated"
	if strings.Contains(string(c.Models[0].Contract["resolutions"]), "mutated") {
		t.Fatal("native descriptor mutation changed the recorded assessment")
	}
}

func TestCompletenessUnchangedMissingExistingVideoContract(t *testing.T) {
	m := core.MediaModel{ID: "video", MediaType: "video", MinDuration: 4, DefaultDuration: 5}
	capture := Capture{Models: []core.MediaModel{m}, IDs: []string{m.ID}}
	ob := structuredContract(m)
	now := time.Now().UTC()
	previous, _ := Reconcile(State{}, capture, []Observation{ob}, now, accepted)
	next, run := Reconcile(previous, capture, []Observation{ob}, now.Add(24*time.Hour), accepted)
	field := coverageField(t, run.Completeness, m.ID, "output.duration")
	if run.Status != "incomplete" || len(run.Changes) != 0 || len(run.OpenWarnings) != 0 || next.Version != previous.Version || !field.Required || field.Status != "unknown" {
		t.Fatal("missing selectable duration contract concealed by quiet run", run)
	}
}

func TestCompletenessOptionalDetailChangeDoesNotInvalidateNativeContract(t *testing.T) {
	m := completeImageModel()
	m.SupportsImageInput = true
	m.MaxImageInputs = 2
	old := structuredContract(m)
	m.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: 2, Image: core.MediaFileLimits{ContentTypes: []string{"image/png"}, MaxBytes: 1000, MinDimension: 1}}
	c := AssessCompleteness(State{}, Capture{Models: []core.MediaModel{m}}, []Observation{old}, nil)
	if c.Status != "partial" {
		t.Fatal("optional detail changed required native contract", c)
	}
	for _, field := range c.Models[0].Fields {
		if field.Required && field.Status != "verified" {
			t.Fatal("existing native fields or effective workflows became unverified", field)
		}
	}
	f := coverageField(t, c, m.ID, "reference_media_limits.max_files")
	if f.Required || f.Status != "unknown" {
		t.Fatal(f)
	}
}

type coverageManifestTransport struct{ body string }

func (p coverageManifestTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(p.body))}, nil
}
func TestRunnerReportsPartialWithoutChangingNativeAvailability(t *testing.T) {
	m := completeImageModel()
	m.SupportsImageInput = true
	m.MaxImageInputs = 2
	repo := &inactiveRunnerRepository{}
	payload := string(encoded(map[string]any{"models": []Entry{{ID: m.ID, Contract: &m}}}))
	runner := Runner{Repo: repo, Providers: &coverageRunnerProvider{model: m}, Client: &http.Client{Transport: coverageManifestTransport{body: payload}}}
	schedule := Schedule{Provider: "fixture", Time: "03:00", Sources: []Source{{ID: "contract", URL: "https://example.test/contract", Parser: "manifest-v1"}}}
	for day := 0; day < 2; day++ {
		if err := runner.check(context.Background(), schedule, Settings{Timezone: "UTC", Email: "report@example.test"}, time.Now().Add(time.Duration(day)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if repo.run.Status != "partial" || repo.run.EmailStatus != "pending" || len(repo.run.Changes) != 0 || len(repo.run.OpenWarnings) != 0 || repo.state.Models[m.ID].Contract == nil || !repo.state.Models[m.ID].Contract.SupportsImageInput {
		t.Fatal("optional file details changed native support or hid the daily partial report", repo.run)
	}
}

func TestRunnerKnownReferenceContractBecomesCompleteAndFailureCannotPass(t *testing.T) {
	m := completeImageModel()
	m.SupportsImageInput = true
	m.MaxImageInputs = 2
	m.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: 2, Image: core.MediaFileLimits{ContentTypes: []string{"image/png"}, MaxBytes: 1000, MinDimension: 1}}
	payload := string(encoded(map[string]any{"models": []Entry{{ID: m.ID, Contract: &m}}}))
	repo := &inactiveRunnerRepository{}
	runner := Runner{Repo: repo, Providers: &coverageRunnerProvider{model: m}, Client: &http.Client{Transport: coverageManifestTransport{body: payload}}}
	schedule := Schedule{Provider: "fixture", Time: "03:00", Sources: []Source{{ID: "contract", URL: "https://example.test/contract", Parser: "manifest-v1"}}}
	for day := 0; day < 2; day++ {
		if err := runner.check(context.Background(), schedule, Settings{Timezone: "UTC", Email: "report@example.test"}, time.Now().Add(time.Duration(day)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if repo.run.Completeness.Status != "complete" || repo.run.Status != "unchanged" || repo.run.EmailStatus != "not_needed" {
		t.Fatal("validated captured/parsed/published reference contract cannot become complete", repo.run)
	}
	good := repo.state
	for _, tc := range []struct {
		name    string
		sources []Source
		body    string
		want    string
	}{
		{"omitted", nil, payload, "stale"},
		{"changed without contract", schedule.Sources, `{"models":[]}`, "stale"},
		{"failed interpretation", schedule.Sources, `not a manifest`, "preserved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo.state = good
			runner.Client = &http.Client{Transport: coverageManifestTransport{body: tc.body}}
			copy := schedule
			copy.Sources = tc.sources
			if err := runner.check(context.Background(), copy, Settings{Timezone: "UTC"}, time.Now()); err != nil {
				t.Fatal(err)
			}
			if repo.run.Completeness.Status != "incomplete" || coverageField(t, repo.run.Completeness, m.ID, "media_type").Status != tc.want {
				t.Fatal("unavailable current evidence passed", repo.run)
			}
		})
	}
}
