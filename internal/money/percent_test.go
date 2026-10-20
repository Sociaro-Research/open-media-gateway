package money

import (
	"encoding/json"
	"testing"
)

func TestMarkupPrecision(t *testing.T) {
	for _, raw := range []string{`-1`, `1000.01`, `0.001`, `null`, `"25"`} {
		var p Percent
		if json.Unmarshal([]byte(raw), &p) == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, tc := range []struct {
		raw        string
		base, want USD
	}{{"25", 1000000, 1250000}, {"12.34", 1000000, 1123400}, {"0", 1, 1}, {"0.01", 1, 2}, {"1000", 0, 0}} {
		var p Percent
		if e := json.Unmarshal([]byte(tc.raw), &p); e != nil {
			t.Fatal(e)
		}
		got, e := p.Apply(tc.base)
		if e != nil || got != tc.want {
			t.Errorf("%s %d: %d %v", tc.raw, tc.base, got, e)
		}
	}
}
