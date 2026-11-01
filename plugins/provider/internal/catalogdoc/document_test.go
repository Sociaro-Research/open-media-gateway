package catalogdoc

import (
	"strings"
	"testing"
)

func TestHasModelRequiresExactTokenBoundaries(t *testing.T) {
	for _, id := range []string{"wan2.7-r2v", "wan2.7-r2v-2026-06-12", "wan3.0-video", "happyhorse-1.1-r2v", "Dreamina Seedance 2.5"} {
		for _, text := range []string{id, "model: \"" + id + "\", next", "(" + id + ")", "use " + id + " for video"} {
			if !HasModel(text, id) {
				t.Errorf("exact token %q not recognized in %q", id, text)
			}
		}
		for _, text := range []string{strings.ToUpper(id), "future-" + id, "other_" + id, "x." + id, "é" + id, id + "-future", id + "_other", id + ".1", id + "2", id + "界"} {
			if HasModel(text, id) {
				t.Errorf("different model %q recognized as %q", text, id)
			}
		}
	}
	if HasModel("unrelated model", "") {
		t.Fatal("empty identity accepted")
	}
	if HasModel("wan2.7-r2v-2026-06-12", "wan2.7-r2v") {
		t.Fatal("snapshot alone inferred alias")
	}
}
