package api

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"testing"
)

type referenceContractFixture struct{ *openAIServiceFixture }

func (s referenceContractFixture) Estimate(context.Context, core.CreateMediaInput) (core.CostEstimate, error) {
	return core.CostEstimate{}, nil
}

func TestGenerationReferencesRejectNestedArrays(t *testing.T) {
	for _, tc := range []struct{ path, model, field string }{
		{"/v1/images/edits", "mock/image", "image"},
		{"/v1/images/generations", "mock/image", "image"},
		{"/v1/videos", "mock/video", "input_reference"},
		{"/v1/generations", "mock/video", "input_reference"},
		{"/v1/generations/estimate", "mock/video", "input_reference"},
	} {
		for _, refs := range []string{`[["https://fixture.invalid/a.png"]]`, `["https://fixture.invalid/a.png",["https://fixture.invalid/b.png"]]`, `[[{"image_url":"https://fixture.invalid/a.png"}]]`, `[[]]`, `[]`, `[null]`, `["https://fixture.invalid/a.png",{"image_url":"https://fixture.invalid/b.png"}]`} {
			t.Run(tc.path+"/"+refs, func(t *testing.T) {
				svc := openAIFixture()
				h := New(referenceContractFixture{svc}, healthStub{}, "secret")
				body := `{"model":"` + tc.model + `","prompt":"test","` + tc.field + `":` + refs + `}`
				w := openAIRequest(h, tc.path, body)
				if refs == `["https://fixture.invalid/a.png",{"image_url":"https://fixture.invalid/b.png"}]` {
					want := 202
					if tc.path == "/v1/generations/estimate" {
						want = 200
					}
					if w.Code != want {
						t.Fatalf("flat references rejected: %d %s", w.Code, w.Body)
					}
					if want == 202 && (len(svc.input.Options.ImageURLs) != 2 || svc.input.Options.ImageURLs[0] != "https://fixture.invalid/a.png" || svc.input.Options.ImageURLs[1] != "https://fixture.invalid/b.png") {
						t.Fatal("reference order changed", svc.input)
					}
				} else {
					var envelope struct{ Error struct{ Param string } }
					_ = json.Unmarshal(w.Body.Bytes(), &envelope)
					if w.Code != 400 || svc.calls != 0 || envelope.Error.Param != tc.field {
						t.Fatalf("nested/invalid reference admitted: %d calls=%d %s", w.Code, svc.calls, w.Body)
					}
				}
			})
		}
	}
}
