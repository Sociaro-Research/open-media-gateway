package branding

import (
	"encoding/json"
	"maps"
	"reflect"
	"testing"
)

func TestDangerDefaultsAreSparseAndIndependent(t *testing.T) {
	s := Defaults()
	for _, p := range []map[string]string{s.Dark, s.Light} {
		for _, role := range []string{"danger", "on-danger"} {
			if _, exists := p[role]; exists {
				t.Fatalf("raw defaults prepopulate %s", role)
			}
		}
		p["accent"], p["highlight"], p["red"] = "#b8dc00", "#00ff00", "#123456"
	}
	dark, light := maps.Clone(s.Dark), maps.Clone(s.Light)
	n := s.Normalize()
	if len(n.Presets) != 1 || n.DefaultPreset != "original" {
		t.Fatal("unexpected catalog", n)
	}
	for i, pair := range [][2]map[string]string{{dark, n.Dark}, {light, n.Light}} {
		for k, v := range pair[0] {
			if pair[1][k] != v {
				t.Fatalf("mode %d changed %s", i, k)
			}
		}
	}
	if n.Dark["danger"] != "#f08088" || n.Dark["on-danger"] != "#171717" || n.Light["danger"] != "#b42336" || n.Light["on-danger"] != "#ffffff" {
		t.Fatal("incorrect fixed defaults", n)
	}
	if !reflect.DeepEqual(s.Dark, dark) || !reflect.DeepEqual(s.Light, light) {
		t.Fatal("normalization mutated input")
	}
	if !reflect.DeepEqual(n, n.Normalize()) {
		t.Fatal("not idempotent")
	}
	if err := s.Validate(); err != nil {
		t.Fatal("legacy omission rejected", err)
	}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	n.Dark["danger"] = "#abcdef"
	if _, exists := s.Dark["danger"]; exists {
		t.Fatal("normalized palette aliases input")
	}
}

func TestDangerExplicitValuesAndInvalidPresence(t *testing.T) {
	for _, role := range []string{"danger", "on-danger"} {
		for _, value := range []string{"", "#abc", "red", "#gg0000"} {
			t.Run(role+"/"+value, func(t *testing.T) {
				s := Defaults()
				s.Dark[role] = value
				if s.Validate() == nil || s.Normalize().Validate() == nil {
					t.Fatal("invalid explicit role accepted")
				}
				if got := s.Normalize().Dark[role]; got != value {
					t.Fatal("invalid value hidden", got)
				}
			})
		}
		for _, scope := range []string{"dark", "light"} {
			s := Defaults()
			if err := json.Unmarshal([]byte(`{"`+scope+`":{"`+role+`":null}}`), &s); err != nil {
				t.Fatal(err)
			}
			if s.Validate() == nil {
				t.Fatal("explicit JSON null accepted", scope, role)
			}
		}
	}
	for _, mutate := range []func(*Settings){
		func(s *Settings) { delete(s.Dark, "text") },
		func(s *Settings) { s.Light = nil },
		func(s *Settings) { s.Dark = map[string]string{} },
		func(s *Settings) { s.Dark["unknown-danger"] = "#123456" },
	} {
		s := Defaults()
		mutate(&s)
		if s.Validate() == nil || s.Normalize().Validate() == nil {
			t.Fatal("legacy mandatory validation weakened")
		}
	}
	s := Defaults()
	s.Dark["danger"], s.Dark["on-danger"] = "#00FF00", "#AbCdEf"
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	n := s.Normalize()
	if n.Dark["danger"] != "#00FF00" || n.Dark["on-danger"] != "#AbCdEf" {
		t.Fatal("custom colors changed")
	}
}

func dangerTestPreset(id, name, dark, light string) Preset {
	s := Defaults()
	s.Dark["danger"], s.Dark["on-danger"] = dark, "#123456"
	s.Light["danger"], s.Light["on-danger"] = light, "#654321"
	return Preset{ID: id, Name: name, Dark: s.Dark, Light: s.Light}
}

func TestPreserveDangerByIDWithoutMutation(t *testing.T) {
	before := Defaults()
	before.Presets = []Preset{dangerTestPreset("a", "Alpha", "#aabbcc", "#ddeeff"), dangerTestPreset("b", "Beta", "#112233", "#445566"), dangerTestPreset("deleted", "Deleted", "#987654", "#abcdef")}
	before.DefaultPreset = "a"
	before = before.Normalize()
	next := Defaults()
	next.Presets = []Preset{dangerTestPreset("b", "Renamed", "#000000", "#000000"), dangerTestPreset("a", "Alpha", "#000000", "#000000"), dangerTestPreset("new", "New", "#000000", "#000000")}
	next.DefaultPreset = "b"
	for _, p := range next.Presets {
		for _, palette := range []map[string]string{p.Dark, p.Light} {
			delete(palette, "danger")
			delete(palette, "on-danger")
		}
	}
	next.Presets[0].Dark["danger"] = "#00ff00"
	beforeJSON, _ := json.Marshal(before)
	nextJSON, _ := json.Marshal(next)
	merged := PreserveDangerColors(next, before)
	if _, exists := merged.Presets[2].Dark["danger"]; exists {
		t.Fatal("new ID inherited an old preset")
	}
	n := merged.Normalize()
	if len(n.Presets) != 3 || n.DefaultPreset != "b" || n.Presets[0].Name != "Renamed" || n.HasPreset("deleted") {
		t.Fatal("catalog membership changed")
	}
	if n.Dark["danger"] != "#00ff00" || n.Dark["on-danger"] != "#123456" || n.Light["danger"] != "#445566" || n.Presets[1].Dark["danger"] != "#aabbcc" || n.Presets[2].Dark["danger"] != "#f08088" || n.Presets[2].Light["danger"] != "#b42336" {
		t.Fatal("preservation did not follow stable IDs", n.Presets)
	}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	gotBefore, _ := json.Marshal(before)
	gotNext, _ := json.Marshal(next)
	if string(gotBefore) != string(beforeJSON) || string(gotNext) != string(nextJSON) {
		t.Fatal("preservation mutated input")
	}
	merged.Presets[0].Name = "Changed"
	merged.Presets[0].Dark["on-danger"] = "#999999"
	merged.Presets[1].Light["text"] = "#999999"
	gotBefore, _ = json.Marshal(before)
	gotNext, _ = json.Marshal(next)
	if string(gotBefore) != string(beforeJSON) || string(gotNext) != string(nextJSON) {
		t.Fatal("preservation leaked aliases")
	}
	for _, invalid := range []string{"", "#abc", "broken"} {
		next.Presets[0].Dark["danger"] = invalid
		if PreserveDangerColors(next, before).Normalize().Validate() == nil {
			t.Fatal("preservation concealed explicit invalid")
		}
	}
	delete(next.Presets[0].Dark, "text")
	delete(next.Presets[0].Dark, "danger")
	if PreserveDangerColors(next, before).Normalize().Validate() == nil {
		t.Fatal("preservation rescued mandatory old role")
	}
}

func TestDangerCataloglessJSONDecodePreservesOmission(t *testing.T) {
	before := Defaults().Normalize()
	before.Presets[0].Dark["danger"], before.Presets[0].Dark["on-danger"] = "#abcdef", "#012345"
	before.Presets[0].Light["danger"], before.Presets[0].Light["on-danger"] = "#fedcba", "#543210"
	// Match the API's decode into Defaults, including Go's map-merge behavior.
	next := Defaults()
	if err := json.Unmarshal([]byte(`{"version":1,"name":"Legacy client","dark":{"accent":"#b8dc00"}}`), &next); err != nil {
		t.Fatal(err)
	}
	for _, p := range []map[string]string{next.Dark, next.Light} {
		for _, role := range []string{"danger", "on-danger"} {
			if _, exists := p[role]; exists {
				t.Fatal("decode masked omitted role", role)
			}
		}
	}
	if err := next.Validate(); err != nil {
		t.Fatal(err)
	}
	// SaveBranding performs catalog-less conversion before the preservation helper.
	next.Presets = []Preset{{ID: "original", Name: "Original", Dark: next.Dark, Light: next.Light}}
	next.DefaultPreset = "original"
	n := PreserveDangerColors(next, before).Normalize()
	if n.Dark["danger"] != "#abcdef" || n.Dark["on-danger"] != "#012345" || n.Light["danger"] != "#fedcba" || n.Light["on-danger"] != "#543210" || n.Dark["accent"] != "#b8dc00" {
		t.Fatal("legacy decode lost custom roles", n)
	}
}
