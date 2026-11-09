package modelcatalog

import (
	"testing"
	"time"
)

func TestEveryReviewedShutdownTakesEffectAtItsBoundary(t *testing.T) {
	for provider, models := range catalog {
		for id, m := range models {
			if m.ShutdownDate == "" {
				continue
			}
			t.Run(provider+"/"+id, func(t *testing.T) {
				cutoff, err := time.Parse("2006-01-02", m.ShutdownDate)
				if m.ShutdownAt != "" {
					cutoff, err = time.Parse(time.RFC3339, m.ShutdownAt)
				}
				if err != nil {
					t.Fatal(err)
				}
				if IsInactive(provider, id, cutoff.Add(-time.Nanosecond)) || !IsInactive(provider, id, cutoff) || !IsInactive(provider, id, cutoff.In(time.FixedZone("test", -7*3600))) {
					t.Fatal("reviewed shutdown did not govern admission", m)
				}
			})
		}
	}
}
