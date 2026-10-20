package core

import "testing"

func TestNormalizeProviderUsagePreservesAndClassifiesMetrics(t *testing.T) {
	usage := NormalizeProviderUsage(map[string]any{
		"total_tokens":  float64(648900),
		"output":        map[string]any{"video_duration": float64(5)},
		"vendor_detail": "kept",
	})
	if usage == nil || len(usage.Raw) == 0 || usage.CostStatus != "metered" {
		t.Fatalf("unexpected usage: %#v", usage)
	}
	if len(usage.Metrics) != 2 {
		t.Fatalf("metrics = %#v", usage.Metrics)
	}
	units := map[string]string{}
	for _, metric := range usage.Metrics {
		units[metric.Name] = metric.Unit
	}
	if units["total_tokens"] != "token" || units["video_duration"] != "second" {
		t.Fatalf("unexpected units: %#v", units)
	}
}
