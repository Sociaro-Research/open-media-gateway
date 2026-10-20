package api

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestGenerationTimeoutPublicContract(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/v1/videos", `{"model":"mock/video","prompt":"scene","timeout_seconds":600}`},
		{"/v1/images/generations", `{"model":"mock/image","prompt":"scene","timeout_seconds":600}`},
		{"/v1/audio/speech", `{"model":"mock/speech","input":"scene","timeout_seconds":600}`},
		{"/v1/audio/generations", `{"model":"mock/music","timeout_seconds":600}`},
		{"/v1/generations", `{"model":"mock/video","prompt":"scene","timeout_seconds":600}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			svc := openAIFixture()
			h := New(svc, healthStub{}, "secret")
			w := openAIRequest(h, tc.path, tc.body)
			if w.Code != 202 || svc.input.Options.TimeoutSeconds == nil || *svc.input.Options.TimeoutSeconds != 600 {
				t.Fatalf("code=%d input=%+v body=%s", w.Code, svc.input, w.Body)
			}
			var output map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &output); err != nil {
				t.Fatal(err)
			}
			if string(output["timeout_seconds"]) != "600" {
				t.Fatalf("missing public timeout: %s", w.Body)
			}
			var options map[string]json.RawMessage
			_ = json.Unmarshal(output["options"], &options)
			if options["timeout_seconds"] != nil {
				t.Fatal("timeout duplicated inside options")
			}
		})
	}
	for _, value := range []string{"0", "-1", "1.5", "null", `"600"`, "true", "9223372037", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			svc := openAIFixture()
			w := openAIRequest(New(svc, healthStub{}, "secret"), "/v1/videos", fmt.Sprintf(`{"model":"mock/video","prompt":"scene","timeout_seconds":%s}`, value))
			if w.Code != 400 || svc.calls != 0 {
				t.Fatalf("code=%d calls=%d body=%s", w.Code, svc.calls, w.Body)
			}
		})
	}
	svc := openAIFixture()
	w := openAIRequest(New(svc, healthStub{}, "secret"), "/v1/videos", `{"model":"mock/video","prompt":"scene","timeout_seconds":9223372036}`)
	if w.Code != 202 {
		t.Fatalf("representable large timeout rejected: %s", w.Body)
	}
}
