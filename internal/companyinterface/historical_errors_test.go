package companyinterface

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHistoricalProxyErrorProjectionDoesNotWalkUserData(t *testing.T) {
	const id = "abcdefab-1111-4111-8111-abcdefabcdef"
	generation := `{"object":"generation","id":"` + id + `","status_url":"/v1/generations/` + id + `","error":{"code":"generation_failed","message":"private-body /private/path","raw":"private-token"},"options":{"parameters":{"object":"generation","error":"user-sentinel"}}}`
	for _, tc := range []struct{ contract, path, raw string }{
		{"generation", "/v1/generations/" + id, generation},
		{"generations", "/v1/generations", `{"object":"list","data":[` + generation + `]}`},
		{"report-detail", "/v1/admin/reports/" + id, `{"generation":` + generation + `}`},
		{"asset", "/v1/references/providers/bytedance/assets/" + id, `{"error":"private-body /private/path","name":"user-sentinel"}`},
		{"assets", "/v1/references/providers/bytedance/assets", `{"data":[{"error":"private-body /private/path","name":"user-sentinel"}]}`},
	} {
		data, err := mediaJSONResponse(Contract{ResponseURLs: tc.contract}, 200, "GET", tc.path, "company-fixture", "https://central.invalid", []byte(tc.raw))
		if err != nil || strings.Contains(string(data), "private-body") || strings.Contains(string(data), "private-token") || !strings.Contains(string(data), "user-sentinel") {
			t.Fatalf("%s: %s %v", tc.contract, data, err)
		}
		var result any
		if json.Unmarshal(data, &result) != nil {
			t.Fatal("invalid proxy JSON")
		}
	}
}
