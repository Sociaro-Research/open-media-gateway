package alibaba

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

const integrityPollSuccess = `{"output":{"task_status":"SUCCEEDED","choices":[{"message":{"content":[{"image":"https://media.example.test/result.png"}]}}]}}`

type integrityBody struct {
	io.Reader
	closed bool
}

func (b *integrityBody) Close() error { b.closed = true; return nil }

type integrityReadError struct{}

func (integrityReadError) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestPollRequiresCompleteBoundedResponse(t *testing.T) {
	const limit = 2 << 20
	for _, tc := range []struct {
		name, body       string
		valid, readError bool
	}{
		{name: "success", body: integrityPollSuccess, valid: true},
		{name: "whitespace", body: " \n" + integrityPollSuccess + "\t", valid: true},
		{name: "exact byte limit", body: integrityPollSuccess + strings.Repeat(" ", limit-len(integrityPollSuccess)), valid: true},
		{name: "second JSON value", body: integrityPollSuccess + `{"output":{}}`},
		{name: "trailing garbage", body: integrityPollSuccess + "<proxy failure>"},
		{name: "truncated second value", body: integrityPollSuccess + `{"error":`},
		{name: "over byte limit", body: integrityPollSuccess + strings.Repeat(" ", limit-len(integrityPollSuccess)+1)},
		{name: "complete prefix then transport failure", body: integrityPollSuccess, readError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reader io.Reader = strings.NewReader(tc.body)
			if tc.readError {
				reader = io.MultiReader(reader, integrityReadError{})
			}
			body := &integrityBody{Reader: reader}
			calls := 0
			adapter := &Adapter{apiKey: "synthetic-key", baseURL: "https://provider.example.test", client: &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/tasks/accepted-task" {
					t.Fatalf("unexpected admission/request: %s %s", r.Method, r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
			})}}
			result, err := adapter.GetTask(context.Background(), "accepted-task")
			if calls != 1 || !body.closed {
				t.Fatalf("calls=%d closed=%v", calls, body.closed)
			}
			if tc.valid {
				if err != nil || result.Status != core.ProviderSucceeded || result.ResultURL != "https://media.example.test/result.png" {
					t.Fatalf("valid response lost: %+v %v", result, err)
				}
			} else if err == nil || !reflect.DeepEqual(result, core.ProviderResult{}) {
				t.Fatalf("invalid response accepted: %+v %v", result, err)
			}
		})
	}
}
