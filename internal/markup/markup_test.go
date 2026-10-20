package markup

import (
	"encoding/json"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"testing"
)

func ptr(p money.Percent) *money.Percent { return &p }
func TestPriorityAndInheritance(t *testing.T) {
	p := Policy{System: Config{Default: ptr(2000), Exceptions: []Rule{{"openai", "", ptr(2500)}, {"openai", "X", ptr(3000)}}}}
	for _, tt := range []struct {
		name    string
		company Config
		want    [3]money.Percent
	}{
		{"inherit", Config{}, [3]money.Percent{3000, 2500, 2000}},
		{"company zero wins over system model", Config{Default: ptr(0)}, [3]money.Percent{0, 0, 0}},
		{"provider zero", Config{Exceptions: []Rule{{"openai", "", ptr(0)}}}, [3]money.Percent{0, 0, 2000}},
		{"model zero", Config{Exceptions: []Rule{{"openai", "X", ptr(0)}}}, [3]money.Percent{0, 2500, 2000}},
		{"company provider wins", Config{Default: ptr(1000), Exceptions: []Rule{{"openai", "", ptr(500)}}}, [3]money.Percent{500, 500, 1000}},
		{"specific exception beats company zero", Config{Default: ptr(0), Exceptions: []Rule{{"openai", "X", ptr(1500)}}}, [3]money.Percent{1500, 0, 0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p.Company = tt.company
			for i, target := range [][2]string{{"openai", "X"}, {"openai", "Y"}, {"alibaba", "X"}} {
				got := p.Resolve(target[0], target[1])
				if got.Percent != tt.want[i] {
					t.Fatalf("%v: %+v, want %v", target, got, tt.want[i])
				}
			}
		})
	}
	p.Company = Config{Default: ptr(900), Exceptions: []Rule{{"openai", "", ptr(500)}, {"openai", "X", ptr(100)}}}
	if r := p.Resolve("openai", "X"); r.Percent != 100 || r.Source != "company_model" {
		t.Fatal(r)
	}
}
func TestInvalidRules(t *testing.T) {
	for _, body := range []string{
		`{"default_percent":null,"version":1}`, `{"default_percent":-1,"version":1}`, `{"default_percent":1.001,"version":1}`, `{"default_percent":0,"version":0}`,
		`{"default_percent":0,"version":1,"exceptions":[{"provider":"openai","markup_percent":null}]}`,
		`{"default_percent":0,"version":1,"exceptions":[{"provider":"openai","markup_percent":0},{"provider":"openai","markup_percent":20}]}`,
	} {
		var c Config
		if err := json.Unmarshal([]byte(body), &c); err == nil && c.Validate(true) == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
