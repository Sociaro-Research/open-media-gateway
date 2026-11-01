package modelcatalog

import (
	"testing"
	"time"
)

func TestReviewedOpenAIRetirementsUseExactIDAndDate(t *testing.T) {
	for id, date := range map[string]string{"gpt-image-1": "2026-10-23", "gpt-audio-mini-2025-10-06": "2026-07-23"} {
		day, _ := time.Parse("2006-01-02", date)
		if IsInactive("openai", id, day.Add(-time.Second)) || !IsInactive("openai", id, day) {
			t.Errorf("retirement boundary missing for %s", id)
		}
		if catalog["openai"][id].ShutdownDate != date {
			t.Errorf("incorrect exact snapshot date: %s", id)
		}
	}
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"gpt-audio-mini", "gpt-audio-mini-2025-12-15", "gpt-image-1-mini", "gpt-image-1.5", "tts-1"} {
		if IsInactive("openai", id, now) {
			t.Errorf("unrelated active model retired: %s", id)
		}
	}
	// A date without an explicit retirement review must never retire a model.
	if IsInactive("openai", "gpt-image-1.5", time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("unreviewed future shutdown became a retirement")
	}
}
