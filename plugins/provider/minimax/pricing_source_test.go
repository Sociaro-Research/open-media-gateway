package minimax

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestOfficialPriceReplayAndReprice(t *testing.T) {
	b, e := os.ReadFile("testdata/pricing.md")
	if e != nil {
		t.Fatal(e)
	}
	src := catalogsync.Source{ID: "pricing", URL: priceSource + ".md", Parser: "document"}
	entries, e := interpretPrices(src, string(b))
	if e != nil || len(entries) != 8 {
		t.Fatal(entries, e)
	}
	a := fixture(t, nil)
	for _, entry := range entries {
		m, _ := modelFor(entry.ID)
		for _, rate := range entry.Rates {
			r := core.MediaRequest{Model: m.ID, Prompt: "scene", Options: core.MediaOptions{Resolution: rate.Resolution, Duration: rate.Duration}}
			if rate.Resolution == "512p" {
				r.Options.ReferenceMode = "first_frame"
				r.Options.ImageURLs = []string{"https://media.example/frame.png"}
			}
			if m.RequiresImageInput {
				r.Options.ReferenceMode = "first_frame"
				r.Options.ImageURLs = []string{"https://media.example/frame.png"}
			}
			if rate.VideoInput != nil && *rate.VideoInput {
				r.Options.ReferenceMode = "references"
				r.Options.VideoURL = "https://media.example/video.mp4"
			}
			q, _, e := a.Quote(context.Background(), m, r)
			if e != nil {
				t.Fatal(m.ID, e)
			}
			updated, e := catalogsync.Reprice(q, rate)
			if e != nil || updated.ExpectedUSD != q.ExpectedUSD {
				t.Fatal(m.ID, rate, q, updated, e)
			}
		}
	}
	for _, edit := range [][2]string{{"per 768P, 10s video", "per 768P, 8s video"}, {"**MiniMax-H3**", "**MiniMax-H3-Turbo**"}, {"**MiniMax-H3-Max**", "**MiniMax-H3-Max-v2**"}, {"first **5 images**", "first **6 images**"}, {"first **2 images**", "first **1 images**"}, {"Output Video\\\n      per second", "Output Video per request"}, {"\\$0.143", "unknown"}, {"768P", "1080P"}, {"Audio input is free", "Audio input is billed"}} {
		changed := strings.Replace(string(b), edit[0], edit[1], 1)
		if changed == string(b) {
			t.Fatalf("fixture mutation did not apply: %q", edit[0])
		}
		if _, e := interpretPrices(src, changed); e == nil {
			t.Fatal("changed billing contract accepted", edit)
		}
	}
	if got, e := interpretPrices(catalogsync.Source{URL: "https://unreviewed.example/prices"}, string(b)); e != nil || len(got) != 0 {
		t.Fatal(got, e)
	}
}

func TestEveryPublishedRateHasAnExecutableRepresentative(t *testing.T) {
	b, e := os.ReadFile("testdata/pricing.md")
	if e != nil {
		t.Fatal(e)
	}
	entries, e := interpretPrices(catalogsync.Source{URL: priceSource + ".md"}, string(b))
	if e != nil {
		t.Fatal(e)
	}
	a := fixture(t, nil)
	for _, entry := range entries {
		m, _ := modelFor(entry.ID)
		for _, rate := range entry.Rates {
			r, e := a.CatalogRateRequest(m, rate)
			if e != nil {
				t.Fatal(entry.ID, rate, e)
			}
			q, _, e := a.Quote(context.Background(), m, r)
			if e != nil {
				t.Fatal(entry.ID, rate, e)
			}
			if _, e = catalogsync.Reprice(q, rate); e != nil {
				t.Fatal(entry.ID, rate, e)
			}
		}
	}
}

func TestOfficialPricesThroughActualCatalogueIngestion(t *testing.T) {
	b, e := os.ReadFile("testdata/pricing.md")
	if e != nil {
		t.Fatal(e)
	}
	source := catalogsync.Source{ID: "pricing", URL: priceSource + ".md", Parser: "document"}
	_, text, e := catalogsync.Parse(source, b)
	if e != nil {
		t.Fatal(e)
	}
	entries, e := interpretPrices(source, text)
	if e != nil || len(entries) != 8 {
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "|") {
				t.Log(line)
			}
		}
		t.Fatalf("normalized source price replay failed: entries=%d err=%v", len(entries), e)
	}
}
