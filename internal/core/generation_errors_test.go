package core_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/diagnostics"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	_ "github.com/Sociaro-Research/open-media-gateway/internal/storage"
	_ "github.com/Sociaro-Research/open-media-gateway/plugins/provider"
)

func TestHistoricalGenerationFailureProjection(t *testing.T) {
	policy := providerkit.NewFailurePolicy("Fixture provider", map[string]providerkit.FailureKind{"Auth": providerkit.FailureCredentials})
	canonical := policy.HTTPFailure(providerkit.FailureSubmit, 401, []byte(`{"code":"Auth"}`)).Error()
	for _, raw := range []string{
		"provider task timed out", canonical, "provider submission failed: " + canonical,
		"provider task failed", "provider fixture-plugin is not configured",
		"OpenAI request failed or timed out; request was not retried", "MiniMax task failed", "DashScope task ended with status FAILED",
		"store provider result: provider result exceeds STORAGE_MAX_BYTES (1024)",
		"provider returned an invalid artifact count", "provider returned an unknown artifact media type",
		"binary artifacts require a private provider source reference", "provider returned malformed artifact metadata",
		"provider primary artifact does not match its scalar result", "Override Sound requires an archived media URL",
		"stored primary artifact does not match its declared media type", "stored artifact does not match its declared media type",
	} {
		safe := core.PublicGenerationFailure(raw)
		if safe == diagnostics.UnknownGeneration || !strings.Contains(strings.ToLower(safe), "check") || core.PublicGenerationFailure(safe) != safe {
			t.Errorf("lost confirmed diagnostic or idempotence: %q => %q", raw, safe)
		}
	}
	for _, raw := range []string{
		"private-body https://private.invalid/?token=private-token /private/server/path",
		canonical + " private-token", "private-token " + canonical,
		strings.Replace(canonical, "[Auth]", "[private-token]", 1),
		"store provider result: /private/server/path", "store provider artifact 02: " + canonical,
		"provider https://private.invalid is not configured", "provider fixture is not configured private-token",
		strings.Repeat("x", diagnostics.MaxStoredMessageBytes+1),
	} {
		if safe := core.PublicGenerationFailure(raw); safe != diagnostics.UnknownGeneration {
			t.Errorf("untrusted stored diagnostic accepted: %q", safe)
		}
	}
}

func TestHistoricalHTTPErrorsDiscardBodyAndKeepSafeCause(t *testing.T) {
	for _, tc := range []struct{ status, action string }{{"400", "inputs"}, {"401", "API key"}, {"403", "permissions"}, {"429", "frequency"}, {"503", "provider status"}} {
		for _, prefix := range []string{"provider submission failed: create ByteDance image: HTTP ", "provider polling failed: get ByteDance video task: HTTP ", "provider submission failed: DashScope HTTP ", "provider polling failed: DashScope HTTP "} {
			safe := core.PublicGenerationFailure(prefix + tc.status + ": private-body https://private.invalid/?token=private-token")
			if !strings.Contains(safe, tc.action) || strings.Contains(safe, "private-") || core.PublicGenerationFailure(safe) != safe {
				t.Fatal("historical HTTP cause lost or body retained", safe)
			}
			if strings.Contains(prefix, "submission") && !strings.Contains(safe, "Acceptance may be unknown") {
				t.Fatal("uncertain submission lost", safe)
			}
		}
	}
}

func TestGenerationProjectionCopiesErrorAndPreservesUserData(t *testing.T) {
	raw := "private-body https://private.invalid/?token=private-token /private/server/path"
	user := map[string]any{"object": "generation", "error": raw, "data": []any{map[string]any{"object": "video", "error": raw}}}
	task := core.Task{Error: &raw, Options: core.MediaOptions{Parameters: user}}
	v := core.GenerationView(task)
	if *v.Task.Error != diagnostics.UnknownGeneration || *task.Error != raw || v.Task.Error == task.Error {
		t.Fatal("projection mutated the saved task or retained its private error")
	}
	v.Error = map[string]any{"code": "private-code", "message": raw, "body": raw}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(b, &fields) != nil {
		t.Fatal("invalid generation JSON")
	}
	e := fields["error"].(map[string]any)
	if len(e) != 2 || e["code"] != "generation_failed" || e["message"] != diagnostics.UnknownGeneration {
		t.Fatal("incompatible or unsafe error envelope", e)
	}
	parameters := fields["options"].(map[string]any)["parameters"]
	if !reflect.DeepEqual(parameters, user) {
		t.Fatal("user parameters were projected", parameters)
	}
}

func TestHistoricalWebhookProjectsOnlyDeclaredGenerationError(t *testing.T) {
	raw := []byte(`{"object":"event","type":"generation.failed","data":{"object":"video","error":{"code":"generation_failed","message":"private-body","extra":"private-token"},"options":{"parameters":{"object":"generation","error":"user-sentinel"}}}}`)
	before := string(raw)
	data, err := core.PublicWebhookPayload(raw)
	if err != nil || strings.Contains(string(data), "private-body") || strings.Contains(string(data), "private-token") || !strings.Contains(string(data), "user-sentinel") || string(raw) != before {
		t.Fatalf("unsafe or destructive webhook projection: %s %v", data, err)
	}
	for _, bad := range [][]byte{[]byte(`{}`), []byte(`null`), []byte(`{"object":"event","type":"other","data":{}}`), append(append([]byte{}, raw...), []byte(` {}`)...)} {
		if _, err := core.PublicWebhookPayload(bad); err != core.ErrStoredWebhookPayload {
			t.Fatal("unsupported persisted envelope was accepted")
		}
	}
}
