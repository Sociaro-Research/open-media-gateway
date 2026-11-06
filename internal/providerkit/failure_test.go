package providerkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"io"
	"strings"
	"testing"
)

func fixtureFailurePolicy() *FailurePolicy {
	return NewFailurePolicy("Fixture provider", map[string]FailureKind{"KnownInput": FailureInput, "KnownCredentials": FailureCredentials, "KnownCapacity": FailureQuota, "KnownBalance": FailureBalance})
}
func TestFailureDiagnosticsAreAllowlistedAndActionable(t *testing.T) {
	p := fixtureFailurePolicy()
	for _, tc := range []struct {
		status int
		kind   FailureKind
		action string
	}{{400, FailureInput, "inputs"}, {401, FailureCredentials, "API key"}, {403, FailurePermission, "permissions"}, {404, FailureConfiguration, "connection"}, {429, FailureRateLimit, "frequency"}, {503, FailureService, "provider status"}} {
		body, _ := json.Marshal(map[string]any{"code": "private-code", "message": "private-body https://private.invalid/?token=private-token", "error": map[string]string{"code": "private-code", "message": "private-body"}})
		e := p.HTTPFailure(FailureSubmit, tc.status, body)
		if e.Kind() != tc.kind || !strings.Contains(e.Error(), tc.action) || !strings.Contains(e.Error(), "before submitting again") {
			t.Fatal(tc.status, e)
		}
		for _, secret := range []string{"private-code", "private-body", "private.invalid", "private-token"} {
			if strings.Contains(e.Error(), secret) {
				t.Fatal("private value in safe diagnostic")
			}
		}
	}
	for code, kind := range map[string]FailureKind{"KnownInput": FailureInput, "KnownCredentials": FailureCredentials, "KnownCapacity": FailureQuota, "KnownBalance": FailureBalance} {
		e := p.NativeFailure(FailurePoll, code)
		if e.Kind() != kind || !strings.Contains(e.Error(), code) || !strings.Contains(e.Error(), "original provider task") {
			t.Fatal(e)
		}
		unknown := p.NativeFailure(FailurePoll, code+".private-suffix")
		if unknown.Kind() != FailureUnknown || strings.Contains(unknown.Error(), code) {
			t.Fatal("prefix allowlist accepted unknown code")
		}
	}
	e := p.HTTPFailure(FailureCheck, 401, []byte("{\"code\":\"KnownInput\"} trailing"))
	if e.Kind() != FailureCredentials || strings.Contains(e.Error(), "KnownInput") {
		t.Fatal("partly decoded body refined status", e)
	}
}
func TestTransportFailuresPreserveOnlySafeClassification(t *testing.T) {
	p := fixtureFailurePolicy()
	private := errors.New("private-token https://private.invalid")
	for _, phase := range []FailurePhase{FailureCheck, FailureSubmit, FailurePoll} {
		e := p.TransportFailure(phase, private)
		if strings.Contains(e.Error(), "private") || !strings.Contains(e.Error(), "network connectivity") {
			t.Fatal(e)
		}
	}
	if e := p.TransportFailure(FailureSubmit, context.DeadlineExceeded); !errors.Is(e, context.DeadlineExceeded) || !strings.Contains(e.Error(), "Acceptance may be unknown") {
		t.Fatal(e)
	}
	if e := p.TransportFailure(FailureSubmit, context.Canceled); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if strings.Contains(p.InvalidLocalRequest().Error(), "Acceptance may be unknown") || strings.Contains(p.InvalidConnection().Error(), "Acceptance may be unknown") {
		t.Fatal("local rejection described as submitted")
	}
	for _, cause := range []error{io.ErrUnexpectedEOF, context.DeadlineExceeded, context.Canceled} {
		e := p.UnreadableResponse(FailureSubmit, fmt.Errorf("private-url: %w", cause))
		if !errors.Is(e, cause) || strings.Contains(e.Error(), "private-url") {
			t.Fatal("lost safe read classification", e)
		}
	}
}
func TestTransportFailureKeepsSafeDestinationClassifications(t *testing.T) {
	p := fixtureFailurePolicy()
	for _, tc := range []struct {
		cause error
		want  string
	}{{httpclient.ErrOutboundDNS, "DNS and network connectivity"}, {httpclient.ErrOutboundPrivateDestination, "exact trusted-host allowlist"}, {httpclient.ErrOutboundDestination, "permitted HTTP(S)"}} {
		e := p.TransportFailure(FailureSubmit, fmt.Errorf("private-url private-token: %w", tc.cause))
		if !errors.Is(e, tc.cause) || !strings.Contains(e.Error(), tc.want) || strings.Contains(e.Error(), "private-url") || strings.Contains(e.Error(), "private-token") {
			t.Fatal(e)
		}
	}
}

func TestHTTPSPolicySentinelPreservesProviderTransportBehavior(t *testing.T) {
	p := fixtureFailurePolicy()
	legacy := p.TransportFailure(FailureSubmit, errors.New("authenticated outbound requests require HTTPS"))
	current := p.TransportFailure(FailureSubmit, fmt.Errorf("private-url private-token: %w", httpclient.ErrOutboundHTTPSRequired))
	var failure *Failure
	if !errors.As(current, &failure) || failure.Kind() != FailureTransport || current.Error() != legacy.Error() {
		t.Fatal("new storage diagnosis changed provider transport behavior", current)
	}
}
func TestFailurePolicyOwnsRegisteredCodesAndTerminalGuidance(t *testing.T) {
	codes := map[string]FailureKind{"KnownInput": FailureInput}
	p := NewFailurePolicy("Fixture provider", codes)
	codes["private-code"] = FailureInput
	if e := p.NativeFailure(FailureTerminal, "private-code"); strings.Contains(e.Error(), "private-code") || e.Kind() != FailureUnknown {
		t.Fatal("policy mutated after registration")
	}
	for _, state := range []string{"canceled", "cancelled", "expired"} {
		e := p.TaskFailure("", state)
		if strings.Contains(e.Error(), "failed lookup") || !strings.Contains(e.Error(), "original provider task and billing") {
			t.Fatal(e)
		}
	}
}
