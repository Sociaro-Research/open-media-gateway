package core

import (
	"context"
	"io"
)

// ProviderArtifact is a normalized media result, not a raw provider response.
// URLs are private temporary sources; the worker must archive every artifact
// before any completed generation exposes a durable content route. ID and
// GroupID are opaque identifiers and must never be used directly as paths.
type ProviderArtifact struct {
	SourceReference string
	Text            string
	ID              string
	MediaType       string
	Format          string
	URL             string
	Duration        float64
	GroupID         string
}

// StoredArtifact is private task persistence. Public views derive only signed or
// authenticated content URLs; storage keys and upstream identities stay private.
type StoredArtifact struct {
	ID          string  `json:"id"`
	ProviderID  string  `json:"provider_id,omitempty"`
	MediaType   string  `json:"media_type"`
	Format      string  `json:"format,omitempty"`
	Duration    float64 `json:"duration,omitempty"`
	GroupID     string  `json:"group_id,omitempty"`
	StorageKey  string  `json:"storage_key"`
	ContentType string  `json:"content_type"`
	SizeBytes   int64   `json:"size_bytes"`
	SHA256      string  `json:"sha256"`
}

type GenerationArtifact struct {
	ID          string  `json:"id"`
	MediaType   string  `json:"media_type"`
	Format      string  `json:"format,omitempty"`
	Duration    float64 `json:"duration,omitempty"`
	GroupID     string  `json:"group_id,omitempty"`
	ContentURL  string  `json:"content_url"`
	ContentType string  `json:"content_type"`
	SizeBytes   int64   `json:"size_bytes"`
	SHA256      string  `json:"sha256"`
}

// ArtifactSource is opened by the resolved provider adapter, never by a client-
// chosen host or a URL carrying credentials. ArchiveSource consumes and closes
// Reader under existing storage size/content limits.
type ArtifactSource struct {
	Reader      io.ReadCloser
	ContentType string
	SizeBytes   int64
}

// ArtifactOpener resolves an opaque owner/actual-connection/revision-bound file
// reference using the provider's trusted authenticated client. It must reject
// foreign or stale references before making a request.
type ArtifactOpener interface {
	OpenArtifact(context.Context, string) (ArtifactSource, error)
}
