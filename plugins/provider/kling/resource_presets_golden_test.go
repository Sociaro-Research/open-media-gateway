package kling

import (
	"net/http"
	"testing"
)

// These synthetic values retain the independent response shape of the saved
// official Query Presets examples (elements4.0 and voice-customization4.0).
// In particular the catalogue is an array of task_result wrappers, not a direct
// array of voices/elements, and the official element source objects may be empty.
func TestOfficialPresetCatalogueDocumentShapedGoldenResponses(t *testing.T) {
	for _, tc := range []struct{ kind, path, raw, subtype string }{
		{"element", "/v1/general/advanced-presets-elements", `{"code":0,"message":"","request_id":"fixture","data":[{"task_id":"fixture-element-task","task_status":"succeed","task_status_msg":"","task_info":{"external_task_id":"fixture"},"task_result":{"elements":[{"element_id":900719925474099312345,"element_name":"Official element","element_description":"Fixture","reference_type":"video_refer","element_image_list":{},"element_video_list":{},"element_voice_info":{},"tag_list":[],"owned_by":"kling","status":"succeed"}]},"created_at":1722769557708,"updated_at":1722769557708}]}`, "video_character"},
		{"voice", "/v1/general/presets-voices", `{"code":0,"message":"","request_id":"fixture","data":[{"task_id":"fixture-voice-task","task_status":"succeed","task_status_msg":"","created_at":1722769557708,"updated_at":1722769557708,"task_result":{"voices":[{"voice_id":"900719925474099312345","voice_name":"Official voice","trial_url":"https://cdn.example/official.mp3","owned_by":"kling"}]}}]}`, "preset"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			a := adapter(t, transport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.URL.Path != tc.path || r.URL.RawQuery != "pageNum=1&pageSize=30" {
					t.Fatal(r.Method, r.URL)
				}
				return response(200, tc.raw), nil
			}))
			page, err := a.ListResources(resourceFixtureContext(), tc.kind, true, 1, 30)
			if err != nil || len(page.Resources) != 1 || !page.Resources[0].IsPreset || page.Resources[0].RemoteID != "900719925474099312345" || page.Resources[0].Subtype != tc.subtype {
				t.Fatal(page, err)
			}
			if tc.kind == "element" && len(page.Resources[0].Sources) != 0 {
				t.Fatal("official empty source object fabricated evidence")
			}
		})
	}
}
