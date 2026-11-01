package catalogsync

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBytePlusRedesignedMediaPrices(t *testing.T) {
	body, err := os.ReadFile("testdata/byteplus-pricing-20261024.md")
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := Parse(Source{Parser: "byteplus-pricing"}, body)
	if err != nil {
		t.Fatal(err)
	}
	rates := map[string][]Rate{}
	for _, e := range entries {
		rates[e.ID] = e.Rates
	}
	for id, n := range map[string]int{"dreamina-seedance-2-5-260628": 6, "dreamina-seedance-2-0-260128": 8, "dreamina-seedance-2-0-fast-260128": 4, "dreamina-seedance-2-0-mini-260615": 4, "seedance-1-5-pro-251215": 2, "seedance-1-0-pro-250528": 1, "seedance-1-0-pro-fast-251015": 1, "dola-seedream-5-0-pro-260628": 3, "dola-seedream-5-0-flash-260915": 1, "seedream-5-0-260128": 1, "seedream-4-5-251128": 1, "seedream-4-0-250828": 1} {
		if len(rates[id]) != n {
			t.Errorf("%s: %d rates, want %d", id, len(rates[id]), n)
		}
	}
	for _, r := range rates["dreamina-seedance-2-5-260628"] {
		want := 10700000
		if r.Resolution == "1080p" {
			want = 11700000
		}
		if r.VideoInput != nil && *r.VideoInput {
			want = 6400000
			if r.Resolution == "1080p" {
				want = 7000000
			}
		}
		if int64(r.Price) != int64(want) || r.Per != 1000000 {
			t.Fatal("wrong video selector/unit", r)
		}
	}
	for _, r := range rates["seedance-1-5-pro-251215"] {
		if r.Audio == nil || *r.Audio && r.Price != 2400000 || !*r.Audio && r.Price != 1200000 {
			t.Fatal("offline or audio rate substituted", r)
		}
	}
	for _, r := range rates["dola-seedream-5-0-pro-260628"] {
		if r.InputImages == nil || r.InputImages.Free != 1 || r.InputImages.Price != 3000 || r.Price != 45000 && r.Price != 90000 {
			t.Fatal("pro input surcharge or layer price lost", r)
		}
	}
	for name, changed := range map[string]string{"missing units": strings.ReplaceAll(string(body), "Token prices are **USD per million tokens**.", ""), "unknown unit": strings.ReplaceAll(string(body), "million tokens", "thousand tokens"), "partial row": strings.Replace(string(body), "|$10.70 |$11.7 |Not supported |Not supported |", "|$10.70 ||Not supported |Not supported |", 1), "unknown headers": strings.ReplaceAll(string(body), "480p and 720p", "480p and 720p and future")} {
		t.Run(name, func(t *testing.T) {
			es, _, err := Parse(Source{Parser: "byteplus-pricing"}, []byte(changed))
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range es {
				if e.ID == "dreamina-seedance-2-5-260628" && len(e.Rates) != 0 {
					t.Fatal("ambiguous/incomplete tariff accepted", e)
				}
			}
		})
	}
}

func TestBytePlusPartialRedesignPreservesConfirmedPrices(t *testing.T) {
	body, err := os.ReadFile("testdata/byteplus-pricing-20261024.md")
	if err != nil {
		t.Fatal(err)
	}
	source := Source{ID: "pricing", Parser: "byteplus-pricing"}
	entries, _, err := Parse(source, body)
	if err != nil {
		t.Fatal(err)
	}
	id := "dreamina-seedance-2-5-260628"
	native := Capture{Models: []core.MediaModel{{ID: id, MediaType: "video"}}, IDs: []string{id}}
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	previous, _ := Reconcile(State{}, native, []Observation{{Source: source, Hash: "complete", Entries: entries}}, now, accepted)
	broken := strings.Replace(string(body), "|$10.70 |$11.7 |Not supported |Not supported |", "|$10.70 ||Not supported |Not supported |", 1)
	partial, _, err := Parse(source, []byte(broken))
	if err != nil {
		t.Fatal(err)
	}
	next, run := Reconcile(previous, native, []Observation{{Source: source, Hash: "partial", Entries: partial}}, now.Add(time.Hour), accepted)
	if len(previous.Models[id].Rates) != 6 || !reflect.DeepEqual(previous.Models[id].Rates, next.Models[id].Rates) {
		t.Fatal("partial table changed a confirmed tariff")
	}
	warned := false
	for _, w := range run.OpenWarnings {
		warned = warned || w.Model == id && strings.Contains(w.Reason, "structured pricing rule")
	}
	if !warned {
		t.Fatal("partial table lost review evidence")
	}
	_, repeat := Reconcile(next, native, []Observation{{Source: source, Hash: "partial", Entries: partial}}, now.Add(2*time.Hour), accepted)
	if len(repeat.Changes) != 0 || len(repeat.NewWarnings) != 0 {
		t.Fatal("partial table repeats mutations", repeat)
	}
}
