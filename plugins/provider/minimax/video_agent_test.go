package minimax

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"net/http"
	"testing"
)

func TestVideoAgentPreservesExplicitPlaceholderArrays(t *testing.T) {
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		p := body(t, r)
		if p["template_id"] != "394875727173492739" || p["media_inputs"] != nil {
			t.Fatal(p)
		}
		texts := p["text_inputs"].([]any)
		if len(texts) != 2 || texts[0].(map[string]any)["value"] != "FLOW" || texts[1].(map[string]any)["value"] != "flowers" {
			t.Fatal(texts)
		}
		return response(200, success("123")), nil
	})
	if _, e := a.CreateVideoAgent(owned("alice"), "video-agent-394875727173492739", nil, []string{"FLOW", "flowers"}); e != nil {
		t.Fatal(e)
	}
}
func TestVideoAgentRejectsWrongKindsAndUndocumentedIDs(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid template input reached provider")
		return nil, nil
	})
	for _, tc := range []struct {
		id            string
		images, texts []string
	}{{"video-agent-unknown", nil, []string{"scene"}}, {"video-agent-392747428568649728", []string{"https://media.example/image.png"}, []string{"ignored text"}}, {"video-agent-394875727173492739", []string{"https://media.example/image.png"}, []string{"word"}}, {"video-agent-393769180141805569", []string{"https://media.example/image.png"}, nil}} {
		if _, e := a.CreateVideoAgent(owned("alice"), tc.id, tc.images, tc.texts); e == nil {
			t.Fatal("invalid template request accepted", tc.id)
		}
	}
}

func TestVideoAgentSecondTextThroughCommonGeneration(t *testing.T) {
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		p := body(t, r)
		texts := p["text_inputs"].([]any)
		if len(texts) != 2 || texts[0].(map[string]any)["value"] != "FLOW" || texts[1].(map[string]any)["value"] != "flowers" {
			t.Fatal(p)
		}
		return response(200, success("123")), nil
	})
	m, _ := modelFor("video-agent-394875727173492739")
	o, e := core.NormalizeModelOptions(m, core.MediaOptions{Parameters: map[string]any{"additional_texts": []string{"flowers"}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Generate(owned("alice"), core.MediaRequest{Model: m.ID, Prompt: "FLOW", Options: o}); e != nil {
		t.Fatal(e)
	}
}

func TestVideoAgentAdditionalTextListIsValidatedAndOrdered(t *testing.T) {
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		p := body(t, r)
		values := p["text_inputs"].([]any)
		for i, want := range []string{"primary", "second", "third"} {
			if values[i].(map[string]any)["value"] != want {
				t.Fatal(values)
			}
		}
		return response(200, success("123")), nil
	})
	m, _ := modelFor("video-agent-394875727173492739")
	o, e := core.NormalizeModelOptions(m, core.MediaOptions{Parameters: map[string]any{"additional_texts": []any{"second", "third"}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Generate(owned("alice"), core.MediaRequest{Model: m.ID, Prompt: "primary", Options: o}); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []any{"not a list", []any{"valid", 5}, make([]string, 64)} {
		if _, e := core.NormalizeModelOptions(m, core.MediaOptions{Parameters: map[string]any{"additional_texts": bad}}); e == nil {
			t.Fatal("invalid placeholder list admitted")
		}
	}
}

func TestVideoAgentRejectsBlankAdditionalTextBeforeAdmission(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("blank template text reached provider")
		return nil, nil
	})
	for _, blank := range []string{"", " ", "\t\n"} {
		r := core.MediaRequest{Model: "video-agent-394875727173492739", Prompt: "primary", Options: core.MediaOptions{Parameters: map[string]any{"additional_texts": []string{blank}}}}
		if err := a.ValidateRequest(owned("alice"), r); err == nil {
			t.Fatal("blank additional text passed admission")
		}
		if _, err := a.Generate(owned("alice"), r); err == nil {
			t.Fatal("blank additional text passed dispatch")
		}
	}
}

func TestVideoAgentCommonOrderedImagesAndAggregateBoundary(t *testing.T) {
	calls := 0
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		p := body(t, r)
		images := p["media_inputs"].([]any)
		texts := p["text_inputs"].([]any)
		if len(images) != 2 || images[0].(map[string]any)["value"] != "https://media.example/first.png" || images[1].(map[string]any)["value"] != "https://media.example/second.png" || len(texts) != 2 || texts[1].(map[string]any)["value"] != "second text" {
			t.Fatal(p)
		}
		return response(200, success("123")), nil
	})
	r := core.MediaRequest{Model: "video-agent-393769180141805569", Prompt: "primary", Options: core.MediaOptions{ImageURLs: []string{"https://media.example/first.png", "https://media.example/second.png"}, Parameters: map[string]any{"additional_texts": []string{"second text"}}}}
	if _, err := a.Generate(owned("alice"), r); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{61, 62} {
		extras := make([]string, n)
		for i := range extras {
			extras[i] = "text"
		}
		r.Options.Parameters["additional_texts"] = extras
		err := a.ValidateRequest(owned("alice"), r)
		if (err == nil) != (n == 61) {
			t.Fatalf("aggregate %d: %v", n+3, err)
		}
	}
	images := make([]string, 64)
	for i := range images {
		images[i] = "https://media.example/image.png"
	}
	imageOnly := core.MediaRequest{Model: "video-agent-392747428568649728", Options: core.MediaOptions{ImageURLs: images}}
	if err := a.ValidateRequest(owned("alice"), imageOnly); err != nil {
		t.Fatal(err)
	}
	imageOnly.Options.ImageURLs = append(images, "https://media.example/overflow.png")
	if err := a.ValidateRequest(owned("alice"), imageOnly); err == nil {
		t.Fatal("65 images admitted")
	}
	if calls != 1 {
		t.Fatal("validation performed provider HTTP")
	}
}
