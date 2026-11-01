package templates

import (
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"os"
	"testing"
)

func TestCommonUIReferenceGrammar(t *testing.T) {
	b, e := os.ReadFile("testdata/ui-reference-grammar.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Prompt  string   `json:"prompt"`
		Kinds   []string `json:"kinds"`
		Invalid bool     `json:"invalid"`
	}
	if e = json.Unmarshal(b, &cases); e != nil {
		t.Fatal(e)
	}
	for _, tc := range cases {
		t.Run(tc.Prompt, func(t *testing.T) {
			slots, err := inferCommon(tc.Prompt, []string{"image", "video", "audio"}, core.ReferenceMode{ID: "references"})
			if (err != nil) != tc.Invalid {
				t.Fatalf("slots=%+v err=%v", slots, err)
			}
			if err == nil && len(slots) != len(tc.Kinds) {
				t.Fatalf("slots=%+v want=%v", slots, tc.Kinds)
			}
		})
	}
}
func TestCommonAuthoringPreservesLegacyRevisions(t *testing.T) {
	m := danceModel()
	legacy := Definition{Title: "Legacy", MediaType: "video", Prompt: "Image 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references"}}
	legacy.Slots, _ = legacy.InferSlots(m)
	if e := legacy.Validate(m); e != nil {
		t.Fatal(e)
	}
	for _, prompt := range []string{"@Picture_1", "<Image1>", "[Image 1]", "{IMAGE_1}"} {
		d := legacy
		d.Syntax = UIReferenceSyntax
		d.Prompt = prompt
		d.Slots, _ = d.InferSlots(m)
		if e := d.Validate(m); e != nil {
			t.Fatal(prompt, e)
		}
		if d.Prompt != prompt {
			t.Fatal("prompt rewritten")
		}
	}
	d := legacy
	d.Syntax = UIReferenceSyntax
	d.ReferenceCounts = map[string]int{"image": 1}
	d.Slots, _ = d.InferSlots(m)
	if e := d.Validate(m); e != nil {
		t.Fatal("explicit ordered legacy prose", e)
	}
	if d.Prompt != "Image 1" {
		t.Fatal("legacy text rewritten")
	}
	d.Prompt = "@Image2"
	d.ReferenceCounts = nil
	if _, e := d.InferSlots(m); e == nil {
		t.Fatal("gapped slots accepted")
	}
	d.Prompt = "@Video1"
	m.ReferenceModes[0].MaxVideos = 0
	if _, e := d.InferSlots(m); e == nil {
		t.Fatal("unsupported type accepted")
	}
}
