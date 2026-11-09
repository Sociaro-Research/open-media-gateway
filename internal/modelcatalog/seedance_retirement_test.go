package modelcatalog

import (
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestSeedanceReviewedMetadataCannotBeOverridden(t *testing.T) {
	const id = "seedance-1-5-pro-251215"
	cutoff := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	model := Enrich("bytedance", core.MediaModel{ID: id, Metadata: &core.ModelMetadata{ShutdownDate: "2030-01-01", ShutdownAt: "2030-01-01T00:00:00Z"}})
	if model.Metadata.ShutdownDate != "2026-11-11" || model.Metadata.ShutdownAt != "2026-11-11T09:00:00Z" {
		t.Fatal("live metadata displaced reviewed retirement", model.Metadata)
	}
	found := false
	for _, model := range Inactive("bytedance", cutoff) {
		found = found || model.ID == id && model.Metadata.ShutdownAt == "2026-11-11T09:00:00Z"
	}
	if !found {
		t.Fatal("retired entry missing from preserved catalogue history")
	}
}

func TestRetirementInstantDoesNotChangeUnrelatedDateOnlyRules(t *testing.T) {
	for _, tc := range []struct{ provider, id, day string }{
		{"bytedance", "seedream-3-0-t2i-250415", "2026-05-13"},
		{"openai", "gpt-image-1", "2026-10-23"},
		{"openai", "gpt-audio-mini-2025-10-06", "2026-07-23"},
	} {
		day, err := time.Parse("2006-01-02", tc.day)
		if err != nil {
			t.Fatal(err)
		}
		if IsInactive(tc.provider, tc.id, day.Add(-time.Nanosecond)) || !IsInactive(tc.provider, tc.id, day) {
			t.Fatal("existing UTC date-only boundary changed", tc)
		}
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	unreviewed := core.ModelMetadata{ShutdownDate: "2026-11-11", ShutdownAt: "2026-11-11T09:00:00Z"}
	if inactiveMetadata("bytedance", "unreviewed-model", unreviewed, now) {
		t.Fatal("unreviewed timestamp became an executable retirement")
	}
	malformed := unreviewed
	malformed.ShutdownAt = "not-a-timestamp"
	malformed.Facts = []core.ModelFact{{Label: "Lifecycle status", Value: "Retired by provider"}}
	if inactiveMetadata("bytedance", "fixture", malformed, now) {
		t.Fatal("malformed timestamp fell back to date-only retirement")
	}
}
