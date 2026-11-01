package kling

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// Retain non-executable provenance so price/discovery synchronization cannot
// propose these utilities as new public generation models.
func (*Adapter) CatalogExclusions() []catalogsync.Exclusion {
	out := []catalogsync.Exclusion{}
	for _, id := range []string{ImageRecognition, FaceDetection, ResourceCreateElement, ResourceCreateVoice, SessionInit, SessionAdd, SessionDelete, SessionClear} {
		reason := "Standalone text/analysis/resource/state operations are outside media generation. Required native preparation remains internal to an authorized media consumer."
		out = append(out, catalogsync.Exclusion{ID: id, Reason: reason, Metadata: &core.ModelMetadata{CheckedAt: "2026-11-01", Facts: []core.ModelFact{{Label: "Product scope", Value: reason}}, Sources: []core.ModelSource{{Title: "Provider API contracts", URL: docsBase}}}})
	}
	return out
}
