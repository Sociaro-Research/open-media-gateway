// Package audit defines management history. Snapshots are for display only;
// access is always checked against current users, memberships and key scopes.
package audit

import (
	"context"
	"encoding/json"
	"time"
)

type Filter struct {
	Scope, OrganizationID, ActorID, Action, ObjectType, ObjectID, Outcome, CorrelationID string
	From, Until                                                                          time.Time
	BeforeID                                                                             int64
	Limit                                                                                int
}
type Event struct {
	ActorPicture     string          `json:"actor_picture,omitempty"`
	ObjectPicture    string          `json:"object_picture,omitempty"`
	ID               int64           `json:"id"`
	At               time.Time       `json:"created_at"`
	Scope            string          `json:"scope"`
	SchemaVersion    int             `json:"schema_version"`
	OrganizationID   string          `json:"organization_id"`
	OrganizationName string          `json:"organization_name"`
	ActorID          string          `json:"actor_id"`
	ActorName        string          `json:"actor_name"`
	ActorEmail       string          `json:"actor_email"`
	ActorType        string          `json:"actor_type"`
	CallingKeyID     string          `json:"calling_key_id"`
	Action           string          `json:"action"`
	ObjectType       string          `json:"object_type"`
	ObjectID         string          `json:"object_id"`
	ObjectName       string          `json:"object_name"`
	Outcome          string          `json:"outcome"`
	CorrelationID    string          `json:"correlation_id"`
	Details          json.RawMessage `json:"details"`
	LegacySource     string          `json:"legacy_source,omitempty"`
	LegacyID         *int64          `json:"legacy_id,omitempty"`
}
type RevokedKey struct {
	ID             string          `json:"id"`
	OrganizationID string          `json:"organization_id"`
	RevokedAt      time.Time       `json:"revoked_at"`
	Snapshot       json.RawMessage `json:"snapshot"`
	// Live spend can reconcile later; it is deliberately separate from the snapshot.
	CurrentSpentMicros  int64 `json:"current_spent_micros"`
	PendingRequests     int64 `json:"pending_requests"`
	UnknownCostRequests int64 `json:"unknown_cost_requests"`
}
type Store interface {
	AuditEvents(context.Context, Filter) ([]Event, error)
	AuditEvent(context.Context, int64, string) (Event, error)
	RevokedKeys(context.Context, Filter) ([]RevokedKey, error)
	AuditFailure(context.Context, string, string, string, string, int) error
}
type correlationKey struct{}

func WithCorrelation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey{}, id)
}
func Correlation(ctx context.Context) string { v, _ := ctx.Value(correlationKey{}).(string); return v }
