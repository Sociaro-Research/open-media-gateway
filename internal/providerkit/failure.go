package providerkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"

	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
)

// FailureKind classifies only reviewed evidence, never provider prose.
type FailureKind uint8

const (
	FailureUnknown FailureKind = iota
	FailureInput
	FailureCredentials
	FailurePermission
	FailureConfiguration
	FailureRateLimit
	FailureQuota
	FailureBalance
	FailureContentPolicy
	FailureService
	FailureTransport
	FailureMalformed
	FailureCanceled
	FailureExpired
)

// FailurePhase separates read-only checks from a potentially accepted submission.
type FailurePhase uint8

const (
	FailureCheck FailurePhase = iota
	FailureSubmit
	FailurePoll
	FailureTerminal
)

type ResponseIssue uint8

const (
	ResponseJSON ResponseIssue = iota
	ResponseRead
	ResponseLimit
	ResponseTaskID
	ResponseMediaURL
	ResponseStatus
)

// FailurePolicy is registered by a trusted adapter using a constant display
// name and an exact reviewed code table. Never supply response/configuration
// values as the policy name or add response-derived codes to its table.
type FailurePolicy struct {
	name  string
	codes map[string]FailureKind
}

func NewFailurePolicy(name string, codes map[string]FailureKind) *FailurePolicy {
	return &FailurePolicy{name: name, codes: maps.Clone(codes)}
}

// Failure holds only safe, bounded classification. It deliberately retains no
// raw response, request URL, transport cause or arbitrary provider message.
type Failure struct {
	kind           FailureKind
	provider       string
	phase          FailurePhase
	status         int
	code           string
	issue          ResponseIssue
	classification error
}

func (e *Failure) Kind() FailureKind    { return e.kind }
func (e *Failure) Is(target error) bool { return errors.Is(e.classification, target) }

func (e *Failure) Error() string {
	name := e.provider
	if name == "" {
		name = "Media provider"
	}
	detail := ""
	if e.status >= 100 && e.status <= 599 {
		detail = fmt.Sprintf(" (HTTP %d)", e.status)
	}
	if e.code != "" {
		detail += " [" + e.code + "]"
	}
	message := "request failed; ask an operator to inspect the original provider task and connection"
	switch e.kind {
	case FailureInput:
		message = "request inputs were rejected; review the selected model's inputs and settings in GET /v1/models"
	case FailureCredentials:
		message = "authentication failed; ask an operator to check the provider API key, region and connection"
	case FailurePermission:
		message = "access was denied; ask an operator to check account, project and model permissions"
	case FailureConfiguration:
		message = "model or endpoint is unavailable; ask an operator to check the connection, region and model activation"
		switch {
		case errors.Is(e.classification, httpclient.ErrOutboundDNS):
			message = "endpoint hostname could not be resolved; ask an operator to verify DNS and network connectivity from Gateway and worker"
		case errors.Is(e.classification, httpclient.ErrOutboundPrivateDestination):
			message = "endpoint is blocked by outbound private-address policy; ask an operator to verify the configured destination and exact trusted-host allowlist"
		case errors.Is(e.classification, httpclient.ErrOutboundDestination):
			message = "endpoint is not a permitted HTTP(S) destination; ask an operator to correct the provider connection endpoint"
		}
	case FailureRateLimit:
		message = "rate or concurrency limit was reached; reduce request frequency and ask an operator to check provider limits"
	case FailureQuota:
		message = "quota or processing capacity limit was reached; reduce request frequency/concurrency and ask an operator to check provider limits and account quota"
	case FailureBalance:
		message = "account balance requires attention; ask an operator to check provider billing and overdue payments"
	case FailureContentPolicy:
		message = "content was rejected by provider policy; review the prompt and reference media"
	case FailureService:
		message = "service is unavailable or timed out; ask an operator to check provider status"
	case FailureTransport:
		message = "connection failed or timed out; ask an operator to check provider network connectivity"
	case FailureMalformed:
		reason := "response is not valid JSON"
		switch e.issue {
		case ResponseRead:
			reason = "response could not be read"
		case ResponseLimit:
			reason = "response exceeds the adapter response-size limit"
		case ResponseTaskID:
			reason = "submission response has no task ID"
		case ResponseMediaURL:
			reason = "completed response has no media URL"
		case ResponseStatus:
			reason = "response has an unknown task status"
		}
		message = reason + "; ask an operator to check provider availability and adapter compatibility"
	case FailureCanceled:
		message = "task was canceled at the provider; check the original task history and billing"
	case FailureExpired:
		message = "task expired at the provider; check the original task history and billing"
	}
	next := ""
	switch e.phase {
	case FailureSubmit:
		next = ". The request was not retried. Acceptance may be unknown; check the original generation and provider history before submitting again."
	case FailurePoll:
		next = ". Check the original provider task before creating another generation; a failed lookup does not prove the task stopped or was uncharged."
	case FailureTerminal:
		next = ". Review the original provider task and billing before starting another generation."
	}
	return name + detail + ": " + message + next
}

// HTTPFailure ignores untrusted message/type/URL fields. Only exact reviewed
// native codes can refine an HTTP status or appear in the safe error.
func (p *FailurePolicy) HTTPFailure(phase FailurePhase, status int, body []byte) *Failure {
	var envelope struct {
		Code   string
		Error  struct{ Code string }
		Output struct{ Code string }
	}
	if json.Unmarshal(body, &envelope) != nil {
		envelope.Code = ""
		envelope.Error.Code = ""
		envelope.Output.Code = ""
	}
	code := envelope.Code
	if code == "" {
		code = envelope.Error.Code
	}
	if code == "" {
		code = envelope.Output.Code
	}
	e := p.NativeFailure(phase, code)
	e.status = status
	if e.kind == FailureUnknown {
		switch {
		case status == 400 || status == 413 || status == 422:
			e.kind = FailureInput
		case status == 401:
			e.kind = FailureCredentials
		case status == 402:
			e.kind = FailureBalance
		case status == 403:
			e.kind = FailurePermission
		case status == 404 || status == 405:
			e.kind = FailureConfiguration
		case status == 429:
			e.kind = FailureRateLimit
		case status == 408 || status >= 500 && status <= 599:
			e.kind = FailureService
		}
	}
	return e
}

// NativeFailure preserves only a code explicitly registered by the adapter.
func (p *FailurePolicy) NativeFailure(phase FailurePhase, code string) *Failure {
	e := &Failure{provider: p.name, phase: phase}
	e.kind = p.codes[code]
	if e.kind != FailureUnknown {
		e.code = code
	}
	return e
}

func (p *FailurePolicy) TaskFailure(code, state string) *Failure {
	e := p.NativeFailure(FailureTerminal, code)
	if e.kind == FailureUnknown {
		switch state {
		case "cancelled", "canceled", "CANCELLED", "CANCELED":
			e.kind = FailureCanceled
		case "expired", "EXPIRED":
			e.kind = FailureExpired
		}
	}
	return e
}

func (p *FailurePolicy) InvalidResponse(phase FailurePhase, issue ResponseIssue) *Failure {
	return &Failure{provider: p.name, phase: phase, kind: FailureMalformed, issue: issue}
}

func (p *FailurePolicy) TransportFailure(phase FailurePhase, err error) error {
	// Preserve cancellation semantics without retaining a wrapped URL/cause.
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}

	kind := FailureTransport
	if errors.Is(err, httpclient.ErrOutboundDestination) {
		kind = FailureConfiguration
	}
	e := &Failure{provider: p.name, phase: phase, kind: kind}
	if errors.Is(err, context.DeadlineExceeded) {
		e.classification = context.DeadlineExceeded
	}
	switch {
	case errors.Is(err, httpclient.ErrOutboundDNS):
		e.classification = httpclient.ErrOutboundDNS
	case errors.Is(err, httpclient.ErrOutboundPrivateDestination):
		e.classification = httpclient.ErrOutboundPrivateDestination
	case errors.Is(err, httpclient.ErrOutboundDestination):
		e.classification = httpclient.ErrOutboundDestination
	}
	return e
}

// InvalidLocalRequest is used only before an HTTP request could be submitted.
func (p *FailurePolicy) InvalidLocalRequest() *Failure {
	return &Failure{provider: p.name, kind: FailureInput}
}
func (p *FailurePolicy) InvalidConnection() *Failure {
	return &Failure{provider: p.name, kind: FailureConfiguration}
}

// UnreadableResponse retains only standard integrity/cancellation sentinels.
func (p *FailurePolicy) UnreadableResponse(phase FailurePhase, err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	e := p.InvalidResponse(phase, ResponseRead)
	if errors.Is(err, context.DeadlineExceeded) {
		e.classification = context.DeadlineExceeded
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		e.classification = io.ErrUnexpectedEOF
	}
	return e
}
