package bytedance

import (
	"context"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPublicFailureDiagnosticsDoNotForwardProviderText(t *testing.T) {
	for _, phase := range []string{"check", "submit", "poll"} {
		for _, status := range []int{400, 401, 402, 403, 404, 429, 500, 503} {
			t.Run(fmt.Sprintf("%s/%d", phase, status), func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					return jsonResponse(status, `{"code":"private-code","message":"private-body https://private.invalid?token=private-token"}`), nil
				})}
				a := &Adapter{apiKey: "fixture-key", baseURL: "https://fixture.invalid", client: client}
				var err error
				switch phase {
				case "check":
					err = a.CheckCredentials(context.Background())
				case "submit":
					_, err = a.Generate(context.Background(), core.MediaRequest{Model: "seedance-model", Prompt: "fixture"})
				case "poll":
					_, err = a.GetTask(context.Background(), "fixture-task")
				}
				if err == nil || calls != 1 {
					t.Fatal(err, calls)
				}
				var safe *providerkit.Failure
				if !errors.As(err, &safe) {
					t.Fatalf("untyped failure: %T", err)
				}
				if !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) {
					t.Fatal("lost status", err)
				}
				for _, secret := range []string{"private-code", "private-body", "private.invalid", "private-token", "fixture-key"} {
					if strings.Contains(err.Error(), secret) {
						t.Fatal("private data escaped")
					}
				}
				if phase == "submit" && !strings.Contains(err.Error(), "before submitting again") {
					t.Fatal("missing acceptance guidance", err)
				}
			})
		}
	}
}
func TestNativeTaskFailuresAndMalformedResponsesAreSafe(t *testing.T) {
	for _, tc := range []struct {
		body  string
		known bool
	}{{`{"status":"failed","error":{"code":"AuthenticationError","message":"private-body"}}`, true}, {`{"status":"private-status"}`, false}, {`{"code":"private-code","message":"private-body"}`, false}, {`{"private-body`, false}} {
		calls := 0
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return jsonResponse(200, tc.body), nil })}
		a := &Adapter{apiKey: "fixture-key", baseURL: "https://fixture.invalid", client: client}
		result, err := a.GetTask(context.Background(), "fixture-task")
		message := result.Error
		if err != nil {
			message = err.Error()
		}
		if message == "" || strings.Contains(message, "private-") || calls != 1 {
			t.Fatal("unsafe/missing task failure", calls)
		}
		if tc.known && !strings.Contains(message, "AuthenticationError") {
			t.Fatal("lost reviewed native classification", message)
		}
	}
	for _, phase := range []string{"submit", "poll"} {
		calls := 0
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("private-transport https://private.invalid?token=private-token")
		})}
		a := &Adapter{apiKey: "fixture-key", baseURL: "https://fixture.invalid", client: client}
		var err error
		if phase == "submit" {
			_, err = a.Generate(context.Background(), core.MediaRequest{Model: "seedance-model", Prompt: "fixture"})
		} else {
			_, err = a.GetTask(context.Background(), "fixture-task")
		}
		if err == nil || strings.Contains(err.Error(), "private-") || calls != 1 {
			t.Fatal("unsafe transport diagnostic", calls)
		}
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", (2<<20)+1))), Header: http.Header{}}, nil
	})}
	a := &Adapter{apiKey: "fixture-key", baseURL: "https://fixture.invalid", client: client}
	_, err := a.GetTask(context.Background(), "fixture-task")
	if err == nil || !strings.Contains(err.Error(), "response-size limit") {
		t.Fatal(err)
	}
}
