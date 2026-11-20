package minimax

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// Exact non-streaming native envelopes. This helper exists only in Go tests.
func H3RejectionFixture(status int) string {
	typ, code := "server_error", "1000"
	switch status {
	case 402:
		typ, code = "insufficient_balance_error", "1008"
	case 422:
		typ, code = "unprocessable_entity_error", "1027"
	}
	return fmt.Sprintf(`{"type":"error","error":{"type":"%s","http_code":"%d","message":"private provider detail"},"created":1792308678,"data":[{"error":{"code":"%s","message":"private image detail"}},{"error":{"code":"%s"}}],"usage":{"generated_images":0,"input_tokens_details":{"text_tokens":0,"image_tokens":0},"output_tokens_details":{"image_tokens":0},"total_tokens":0,"input_tokens":0,"output_tokens":0}}`, typ, status, code, code)
}

func TestH3ZeroChargeRequiresExactEvidence(t *testing.T) {
	for _, status := range []int{402, 422, 500} {
		raw := H3RejectionFixture(status)
		if !h3RejectionProvesNoCharge([]byte(raw), status, 2) {
			t.Fatal("valid native response rejected", status)
		}
		minimal := strings.Replace(raw, `,"total_tokens":0,"input_tokens":0,"output_tokens":0`, "", 1)
		if !h3RejectionProvesNoCharge([]byte(minimal), status, 2) {
			t.Fatal("unused aggregates required", status)
		}
		for _, bad := range []struct{ from, to string }{
			{`"created":1792308678`, `"created":null`}, {`"created":1792308678,`, ""}, {`"type":"error"`, `"type":"result"`},
			{`"http_code":`, `"other_code":`}, {`"generated_images":0`, `"generated_images":1`}, {`"generated_images":0,`, ""},
			{`"text_tokens":0`, `"text_tokens":"0"`}, {`"text_tokens":0`, `"text_tokens":1,"text_tokens":0`}, {`"text_tokens":0,`, ""},
			{`"input_tokens_details":{"text_tokens":0,"image_tokens":0}`, `"input_tokens_details":{"text_tokens":0}`},
			{`"output_tokens_details":{"image_tokens":0}`, `"output_tokens_details":{}`},
			{`"image_tokens":0`, `"image_tokens":false`}, {`"total_tokens":0`, `"total_tokens":null`}, {`"input_tokens":0`, `"input_tokens":1`},
			{`"output_tokens":0`, `"output_tokens":0.0`}, {`"data":[{`, `"data":[{"url":"https://output.fixture/image.png",`},
			{`"data":[{`, `"data":[{"b64_json":"AAAA",`}, {`"error":{"code":`, `"error":{"message":`},
		} {
			if h3RejectionProvesNoCharge([]byte(strings.Replace(raw, bad.from, bad.to, 1)), status, 2) {
				t.Fatalf("unverified evidence accepted for %d, %s", status, bad.from)
			}
		}
		if h3RejectionProvesNoCharge([]byte(raw), status, 3) || h3RejectionProvesNoCharge([]byte(raw), 401, 2) {
			t.Fatal("mismatched response classified")
		}
	}
	if h3RejectionProvesNoCharge([]byte(strings.ReplaceAll(H3RejectionFixture(500), "1000", "1027")), 500, 2) {
		t.Fatal("server status contradicts all safety failures")
	}
}

func TestH3RejectedPreDispatchKeepsOrdinaryValidation(t *testing.T) {
	calls := 0
	adapter := &h3Images{Adapter: fixture(t, func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected dispatch")
	})}
	task, err := adapter.Generate(owned("fixture"), core.MediaRequest{Model: h3Image, Prompt: "fixture", Options: core.MediaOptions{OutputCount: 11}})
	if calls != 0 || len(task.Artifacts) != 0 || !errors.Is(err, core.ErrOptionsInvalid) || errors.Is(err, core.UnchargedImageRejection{}) || errors.Is(err, core.ErrProviderSubmissionUncertain) {
		t.Fatalf("pre-dispatch validation calls=%d error=%v", calls, err)
	}
}
