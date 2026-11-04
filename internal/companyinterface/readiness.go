package companyinterface

import "context"

// These private types have no production constructor or setter. The test-only
// fixture constructor lives in export_test.go, allowing real HTTP compositions
// to model future readiness without a runtime flag, header or configuration.
type serverReadinessKey struct{}
type serverReadinessFixture struct{ current func() Protocol }

// ServerProtocol reports this implementation, independently of caller claims.
// No production request can supply the private fixture value.
func ServerProtocol(ctx context.Context) Protocol {
	if fixture, ok := ctx.Value(serverReadinessKey{}).(serverReadinessFixture); ok && fixture.current != nil {
		p := fixture.current()
		p.Capabilities = append([]string(nil), p.Capabilities...)
		return p
	}
	return ImplementedProtocol()
}
