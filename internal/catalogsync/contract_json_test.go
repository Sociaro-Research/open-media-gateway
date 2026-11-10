package catalogsync

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestContractJSONComparison(t *testing.T) {
	number := 8
	var absent *int
	for _, tc := range []struct {
		name string
		a, b any
		same bool
	}{
		{"integer float", 8, float64(8), true},
		{"integer widths", int8(8), uint64(8), true},
		{"decimal exponent", json.Number("8.00e+0"), 8, true},
		{"negative", -8, json.Number("-80e-1"), true},
		{"fraction", 0.125, json.Number("125e-3"), true},
		{"decimal float", 0.1, json.Number("0.100"), true},
		{"signed zero", math.Copysign(0, -1), 0, true},
		{"large integer", uint64(9007199254740993), json.Number("9007199254740993.0"), true},
		{"maximum uint64", uint64(math.MaxUint64), json.Number("18446744073709551615"), true},
		{"minimum int64", int64(math.MinInt64), json.Number("-9223372036854775808"), true},
		{"large integer change", uint64(9007199254740992), uint64(9007199254740993), false},
		{"rounded float", uint64(9007199254740993), float64(9007199254740993), false},
		{"large fractional change", json.Number("9007199254740993.1"), json.Number("9007199254740993.2"), false},
		{"huge exponent", json.Number("1e999999999999999999999"), json.Number("10e999999999999999999998"), true},
		{"tiny exponent change", json.Number("1e-999999999999999999999"), json.Number("2e-999999999999999999999"), false},
		{"real numeric change", 8, 8.5, false},
		{"string number", "8", 8, false},
		{"boolean number", false, 0, false},
		{"nested containers", map[string]any{"x": []any{8, map[string]int{"n": -2}}}, map[string]any{"x": []any{8.0, map[string]any{"n": -2.0}}}, true},
		{"nested change", []any{map[string]int{"x": 8}}, []any{map[string]int{"x": 9}}, false},
		{"ordered arrays", []int{1, 2}, []float64{2, 1}, false},
		{"typed arrays", []int{1, 2}, []float64{1, 2}, true},
		{"pointer value", &number, 8.0, true},
		{"typed nil", absent, nil, true},
		{"null object key", map[string]any{"x": nil}, map[string]any{"y": nil}, false},
		{"nil empty arrays", []int(nil), []int{}, false},
		{"invalid number", json.Number("bad"), json.Number("bad"), false},
		{"nonfinite", math.Inf(1), math.Inf(1), false},
		{"unsupported value", make(chan int), make(chan int), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameContract(tc.a, tc.b); got != tc.same || sameContract(tc.b, tc.a) != tc.same {
				t.Fatalf("comparison: %T(%v) / %T(%v): got %v want %v", tc.a, tc.a, tc.b, tc.b, got, tc.same)
			}
		})
	}
	for _, raw := range []string{"", "{", "null null", "01", "NaN"} {
		if sameJSON(json.RawMessage(raw), json.RawMessage(raw)) {
			t.Fatalf("invalid JSON compared equal: %q", raw)
		}
	}
	// Respect the existing contract JSON rules for omitted slices/defaults.
	if !sameContract(core.ParameterField{}, core.ParameterField{Choices: []string{}}) || sameContract(core.ParameterField{}, core.ParameterField{Default: 0}) {
		t.Fatal("omitted defaults or empty optional slices changed meaning")
	}
}

func TestReconcileNumericContractEvidenceAndRealChanges(t *testing.T) {
	model := completeImageModel()
	model.Metadata = &core.ModelMetadata{Description: "Numeric contract fixture"}
	model.Parameters = []core.ParameterField{{Key: "amount", Type: "number", Default: 8}}
	capture := Capture{Models: []core.MediaModel{model}, IDs: []string{model.ID}}
	ob := structuredContract(model)
	ob.Entries[0].Metadata = model.Metadata
	ob.Entries = append(ob.Entries, Entry{ID: "unreviewed"})
	ob.Entries[0].Rates = []Rate{{Unit: "image", Per: 1, Price: 123456, Region: "global"}}
	// The source snapshot is independent of subsequent native descriptor edits.
	data, err := json.Marshal(ob)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot Observation
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	ob = snapshot
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	state, first := Reconcile(State{}, capture, []Observation{ob}, now, accepted)
	if state.Version != 1 || len(first.NewWarnings) == 0 || len(state.Models[model.ID].Rates) != 1 {
		t.Fatalf("initial evidence missing: %+v", first)
	}
	baseline := state.Models[model.ID]
	for i, value := range []any{8.0, json.Number("8.00"), int64(8)} {
		capture.Models[0].Parameters = []core.ParameterField{{Key: "amount", Type: "number", Default: value}}
		next, run := Reconcile(state, capture, []Observation{ob}, now.Add(time.Duration(i+1)*time.Hour), accepted)
		if next.Version != 1 || len(run.Changes) != 0 || len(run.NewWarnings) != 0 || len(run.ResolvedWarnings) != 0 || run.EmailStatus != "not_needed" {
			t.Fatalf("equivalent persisted/observed default created changes: %+v", run)
		}
		entry := next.Models[model.ID]
		if !reflect.DeepEqual(entry.Rates, baseline.Rates) || entry.Inactive || entry.Contract == nil || len(entry.Metadata.Sources) == 0 || !reflect.DeepEqual(entry.Metadata.Sources, baseline.Metadata.Sources) || next.Observations[ob.Source.ID].Hash != ob.Hash {
			t.Fatal("tariff, activation or source evidence changed")
		}
		if len(run.OpenWarnings) != len(first.OpenWarnings) || run.OpenWarnings[0].Occurrences != i+2 || run.Status != "warning" || run.Completeness.Status != first.Completeness.Status {
			t.Fatal("independent warnings/completeness were hidden", run)
		}
		state = next
	}
	// A real default change must create one version and an observation mismatch.
	capture.Models[0].Parameters = []core.ParameterField{{Key: "amount", Type: "number", Default: 9}}
	changed, run := Reconcile(state, capture, []Observation{ob}, now.Add(4*time.Hour), accepted)
	if changed.Version != 2 || len(run.Changes) != 1 || run.Changes[0].Field != "contract" || len(run.NewWarnings) != 1 {
		t.Fatalf("real change missed: %+v", run)
	}
	// The original observation is intentionally retained, with its warning.
	quiet, run := Reconcile(changed, capture, []Observation{ob}, now.Add(5*time.Hour), accepted)
	if quiet.Version != 2 || len(run.Changes) != 0 || len(run.NewWarnings) != 0 || len(run.OpenWarnings) != len(first.OpenWarnings)+1 {
		t.Fatalf("changed contract replay oscillated: %+v", run)
	}
	// Retirement and reactivation still change the executable contract.
	capture.Inactive, capture.Models = capture.Models, nil
	retired, run := Reconcile(quiet, capture, nil, now.Add(6*time.Hour), accepted)
	if retired.Version != 3 || retired.Models[model.ID].Contract != nil || !retired.Models[model.ID].Inactive || len(run.Changes) != 2 || !reflect.DeepEqual(retired.Models[model.ID].Rates, baseline.Rates) {
		t.Fatalf("retirement changed: %+v", run)
	}
	capture.Models, capture.Inactive = capture.Inactive, nil
	active, run := Reconcile(retired, capture, nil, now.Add(7*time.Hour), accepted)
	if active.Version != 4 || active.Models[model.ID].Contract == nil || active.Models[model.ID].Inactive || len(run.Changes) != 2 {
		t.Fatalf("reactivation changed: %+v", run)
	}
}

func TestManifestContractNumbersRemainExact(t *testing.T) {
	source := Source{ID: "manifest", URL: "https://example.test/models", Parser: "manifest-v1"}
	body := []byte(`{"models":[{"id":"m","contract":{"id":"m","parameters":[{"key":"value","default":{"large":9007199254740993,"fraction":0.1234567890123456789}}]}}]}`)
	entries, _, err := Parse(source, body)
	if err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(entries[0].Contract.Parameters[0].Default)
	if err != nil || string(value) != `{"fraction":0.1234567890123456789,"large":9007199254740993}` {
		t.Fatalf("manifest default rounded: %s %v", value, err)
	}
	// Strict decoding must still reject malformed/unknown contract fields.
	for _, raw := range []string{
		`{"models":[{"id":"m","contract":{"id":"m","unexpected":1}}]}`,
		`{"models":[{"id":"m","contract":{"id":"m","parameters":[{"key":"value","default":01}]}}]}`,
	} {
		if _, _, err := Parse(source, []byte(raw)); err == nil {
			t.Fatalf("invalid manifest accepted: %s", raw)
		}
	}
}
