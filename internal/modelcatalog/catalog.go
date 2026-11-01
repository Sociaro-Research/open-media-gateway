// Package modelcatalog stores reviewed public metadata independently of account
// credentials. Native adapters remain the authority for supported options.
package modelcatalog

import (
	"embed"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"time"
)

//go:embed catalog.json
var files embed.FS
var catalog = func() map[string]map[string]core.ModelMetadata {
	data, err := files.ReadFile("catalog.json")
	if err != nil {
		panic(err)
	}
	var result map[string]map[string]core.ModelMetadata
	if err := json.Unmarshal(data, &result); err != nil {
		panic(err)
	}
	return result
}()

// Inactive returns reviewed deactivated versions for catalogue history. These
// records must never be added to an adapter's executable model list.
func Inactive(provider string, now time.Time) []core.MediaModel {
	var models []core.MediaModel
	for id := range catalog[provider] {
		if IsInactive(provider, id, now) {
			models = append(models, Enrich(provider, core.MediaModel{ID: id}))
		}
	}
	return models
}

// IsInactive requires an explicit lifecycle review, not an inferred date or
// an account-specific discovery omission. Historical metadata stays available.
func IsInactive(provider, id string, now time.Time) bool {
	m := catalog[provider][id]
	if m.ShutdownDate == "" || m.ShutdownDate > now.UTC().Format("2006-01-02") {
		return false
	}
	if provider == "bytedance" && id == "seedream-3-0-t2i-250415" {
		return true
	}
	if provider == "alibaba" || provider == "openai" {
		for _, f := range m.Facts {
			if f.Label == "Lifecycle status" && f.Value == "Retired by provider" {
				return true
			}
		}
	}
	return false
}

// Enrich returns independent slices: no account metadata is ever written into
// the shared catalogue or another account's cached model descriptor.
func Enrich(provider string, model core.MediaModel) core.MediaModel {
	if model.ReferencePromptRecommendation == nil {
		model.ReferencePromptRecommendation = ReferenceRecommendation(provider, model.ID)
	} else {
		model.ReferencePromptRecommendation = model.ReferencePromptRecommendation.Clone()
	}
	metadata, ok := catalog[provider][model.ID]
	if !ok && model.Metadata == nil {
		return model
	}
	metadata.Facts = append([]core.ModelFact(nil), metadata.Facts...)
	metadata.Sources = append([]core.ModelSource(nil), metadata.Sources...)
	if live := model.Metadata; live != nil {
		metadata.APICreated, metadata.OwnedBy = live.APICreated, live.OwnedBy
		if live.ShutdownDate != "" {
			metadata.ShutdownDate = live.ShutdownDate
		}
		metadata.Facts = append(metadata.Facts, live.Facts...)
	}
	model.Metadata = &metadata
	return model
}
