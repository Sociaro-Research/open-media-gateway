package templates

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTemplateCurrentContentLegacyAliases(t *testing.T) {
	for _, visible := range []bool{false, true} {
		current := Definition{Title: "Current", Prompt: "Current prompt"}
		record := Record{ID: "fixture", Draft: current}
		if visible {
			record.Published = &Definition{Title: "Obsolete", Prompt: "Must never escape"}
		}
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err = json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if wire["is_published"] != visible || !reflect.DeepEqual(wire["content"], wire["draft"]) {
			t.Fatal("current aliases diverged")
		}
		if visible && !reflect.DeepEqual(wire["content"], wire["published"]) {
			t.Fatal("obsolete published copy was serialized")
		}
		if !visible && wire["published"] != nil {
			t.Fatal("off template exposed a publication")
		}
	}
}
