package modelcatalog

import (
	"testing"
	"time"
)

// This regression uses only APIs present at the base SHA. It can be added and
// run before the implementation to demonstrate the semantic failure.
func TestSeedanceExactCutoffBaselineRegression(t *testing.T) {
	const id = "seedance-1-5-pro-251215"
	cutoff := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		now      time.Time
		inactive bool
	}{
		{"same UTC date at midnight", cutoff.Add(-9 * time.Hour), false},
		{"one nanosecond before", cutoff.Add(-time.Nanosecond), false},
		{"exact cutoff", cutoff, true},
		{"one nanosecond after", cutoff.Add(time.Nanosecond), true},
		{"equivalent UTC+8 before", cutoff.Add(-time.Nanosecond).In(time.FixedZone("UTC+8", 8*3600)), false},
		{"equivalent UTC+8 at cutoff", cutoff.In(time.FixedZone("UTC+8", 8*3600)), true},
		{"equivalent UTC-7 after", cutoff.Add(time.Nanosecond).In(time.FixedZone("UTC-7", -7*3600)), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsInactive("bytedance", id, tc.now); got != tc.inactive {
				t.Fatalf("IsInactive(%s) = %t; want %t", tc.now.Format(time.RFC3339Nano), got, tc.inactive)
			}
		})
	}
	for _, active := range []string{"dreamina-seedance-2-0-mini-260615", "dreamina-seedance-2-0-260128", "dreamina-seedance-2-5-260628", "seedance-1-5-pro"} {
		if IsInactive("bytedance", active, cutoff.Add(365*24*time.Hour)) {
			t.Fatal("retirement escaped exact ID", active)
		}
	}
}
