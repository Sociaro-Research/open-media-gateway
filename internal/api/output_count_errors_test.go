package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// A count below one, or one that is not an integer, exceeds nothing: only a
// count above the model's max_outputs may be reported as exceeding it.
func TestImageCountBelowOneIsNotReportedAsExceedingTheModel(t *testing.T) {
	for _, path := range []string{"/v1/images/generations", "/v1/generations", "/v1/generations/estimate"} {
		for _, tc := range []struct{ n, want, never string }{
			{"0", "at least 1", "exceeds"},
			{"-1", "at least 1", "exceeds"},
			{"1.5", "at least 1", "exceeds"},
			{`"2"`, "at least 1", "exceeds"},
			{"null", "at least 1", "exceeds"},
			{"2", "exceeds the selected model output count capability", "at least 1"},
		} {
			t.Run(path+"/"+tc.n, func(t *testing.T) {
				svc := openAIFixture()
				w := openAIRequest(New(svc, healthStub{}, "secret"), path, `{"model":"mock/image","prompt":"x","n":`+tc.n+`}`)
				var got struct {
					Error struct{ Code, Message, Param string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err, w.Body)
				}
				if w.Code != 400 || got.Error.Code != "invalid_request" || got.Error.Param != "n" || svc.calls != 0 {
					t.Fatalf("envelope changed or request admitted: %d %s calls=%d", w.Code, w.Body, svc.calls)
				}
				if !strings.Contains(got.Error.Message, tc.want) || strings.Contains(got.Error.Message, tc.never) {
					t.Fatalf("n=%s: got %q; want a message containing %q and not %q", tc.n, got.Error.Message, tc.want, tc.never)
				}
			})
		}
	}
}
