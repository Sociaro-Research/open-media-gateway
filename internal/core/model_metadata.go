package core

// ModelMetadata separates published release dates, version dates and API object
// creation times. They must never be silently presented as the same date.
type ModelMetadata struct {
	Description  string        `json:"description,omitempty"`
	ReleaseDate  string        `json:"release_date,omitempty"`
	VersionDate  string        `json:"version_date,omitempty"`
	Snapshot     string        `json:"snapshot,omitempty"`
	APICreated   int64         `json:"api_created,omitempty"`
	OwnedBy      string        `json:"owned_by,omitempty"`
	ShutdownDate string        `json:"shutdown_date,omitempty"`
	CheckedAt    string        `json:"checked_at,omitempty"`
	Facts        []ModelFact   `json:"facts,omitempty"`
	Sources      []ModelSource `json:"sources,omitempty"`
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
