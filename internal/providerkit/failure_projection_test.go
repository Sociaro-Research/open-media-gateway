package providerkit

import (
	"strings"
	"testing"
)

func TestStoredFailureRequiresCompleteTrustedForm(t *testing.T) {
	p := NewFailurePolicy("Projection fixture", map[string]FailureKind{"Auth": FailureCredentials}, "submit fixture: ")
	for _, e := range []*Failure{
		p.HTTPFailure(FailureSubmit, 401, []byte(`{"code":"Auth"}`)),
		p.HTTPFailure(FailurePoll, 503, nil), p.InvalidResponse(FailurePoll, ResponseStatus),
	} {
		raw := e.Error()
		for _, input := range []string{raw, "submit fixture: " + raw} {
			if safe, ok := p.ProjectStored(input); !ok || safe != raw {
				t.Fatal("lost safe typed failure", input)
			}
		}
		for _, input := range []string{raw + " https://private.invalid/", "private-token " + raw, "submit fixture: submit fixture: " + raw,
			strings.Replace(raw, "HTTP 401", "HTTP 0401", 1) + "x", strings.Replace(raw, "[Auth]", "[private-token]", 1) + "x"} {
			if _, ok := p.ProjectStored(input); ok {
				t.Fatal("accepted untrusted format", input)
			}
		}
	}
	raw := p.HTTPFailure(FailureSubmit, 401, []byte(`{"code":"Auth"}`)).Error()
	if _, ok := p.ProjectStored(strings.Replace(raw, "authentication failed", "access was denied", 1)); ok {
		t.Fatal("accepted mismatched reason")
	}
}
