package core

import "encoding/json"

// ModelMetadata separates published release dates, version dates and API object
// creation times. They must never be silently presented as the same date.
type ModelMetadata struct {
	// Operational records last-confirmed public facts, never executable controls.
	// Current freshness/uncertainty belongs to the catalogue run's coverage.
	Operational  map[string]OperationalFact `json:"operational,omitempty"`
	Description  string                     `json:"description,omitempty"`
	ReleaseDate  string                     `json:"release_date,omitempty"`
	VersionDate  string                     `json:"version_date,omitempty"`
	Snapshot     string                     `json:"snapshot,omitempty"`
	APICreated   int64                      `json:"api_created,omitempty"`
	OwnedBy      string                     `json:"owned_by,omitempty"`
	ShutdownDate string                     `json:"shutdown_date,omitempty"`
	CheckedAt    string                     `json:"checked_at,omitempty"`
	Facts        []ModelFact                `json:"facts,omitempty"`
	Sources      []ModelSource              `json:"sources,omitempty"`
}

type ModelFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type ModelSource struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

func (m MediaModel) CatalogDate() string {
	if m.Metadata == nil {
		return ""
	}
	// A dated snapshot is useful when the alias has been updated since launch.
	if m.Metadata.VersionDate > m.Metadata.ReleaseDate {
		return m.Metadata.VersionDate
	}
	return m.Metadata.ReleaseDate
}

// OperationalFact retains typed source data and exact normalized provenance.
type OperationalFact struct {
	Value     json.RawMessage `json:"value"`
	Source    ModelSource     `json:"source"`
	Snapshot  string          `json:"snapshot"`
	CheckedAt string          `json:"checked_at"`
}
