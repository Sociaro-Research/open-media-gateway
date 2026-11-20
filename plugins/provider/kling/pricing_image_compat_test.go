package kling

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func currentImagePriceFixture(legacy string) string {
	lines := strings.Split(legacy, "\n")
	out := make([]string, 0, len(lines)+14)
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			out = append(out, line)
			continue
		}
		cells := normalizePriceCells(line)
		switch {
		case cells[0] == "Model":
			out = append(out, "| "+imagePriceHeaderCurrent+" |")
		case isPriceSeparator(cells):
			out = append(out, "|---|---|---|---|---|---|---|")
			keys := make([]string, 0, 14)
			for k := range reviewedImage4EvidenceKeys() {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				parts := strings.Split(k, "|")
				out = append(out, fmt.Sprintf("| Kling Image 4.0 | %s | %s | %s | %s | %s | 16 Units ($0.063) / image |", parts[0], image4InputOutputBilling, image4InputPrice, parts[1], parts[2]))
			}
		default:
			billing := imagePriceOutputBilling
			if cells[1] == "AI Multi-Shot" {
				billing = "Billed per call"
			}
			out = append(out, fmt.Sprintf("| %s | %s | %s | - | - | %s | %s |", cells[0], cells[1], billing, cells[2], cells[3]))
		}
	}
	return strings.Join(out, "\n")
}

func TestImagePricingSchemaCompatibility(t *testing.T) {
	source, old := pricingFixture(t, "image")
	current := currentImagePriceFixture(old)
	if path := os.Getenv("KLING_REVIEWED_IMAGE_PRICING_CAPTURE"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != "ddae2ee5da1453db41245f805e5e36256c2d2b2953485928b2804814903b3e04" {
			t.Fatalf("source evidence changed: %s", got)
		}
		current = string(raw)
	}
	observe := func(body string) (catalogsync.Observation, error) {
		_, normalized, err := catalogsync.Parse(source, []byte(body))
		if err != nil {
			return catalogsync.Observation{}, err
		}
		entries, err := interpretPrices(source, normalized)
		return catalogsync.Observation{Source: source, Entries: entries, Text: normalized, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(normalized)))}, err
	}
	legacy, err := observe(old)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := observe(current)
	if err != nil {
		t.Fatal(err)
	}
	legacyRows := map[string]bool{}
	withoutColumns := func(entries []catalogsync.Entry, column string) []catalogsync.Entry {
		t.Helper()
		out := append([]catalogsync.Entry(nil), entries...)
		for i, e := range entries {
			var facts []klingPublishedTariff
			if err := json.Unmarshal(e.OperationalFields["kling_tariff_contract"], &facts); err != nil {
				t.Fatal(err)
			}
			for j, fact := range facts {
				if fact.SourceColumn != column || strings.HasPrefix(fact.Row, "Kling Image 4.0") {
					t.Fatalf("incorrect observation: %+v", fact)
				}
				legacyRows[fact.Row] = true
				facts[j].SourceColumn = ""
			}
			raw, err := json.Marshal(facts)
			if err != nil {
				t.Fatal(err)
			}
			out[i].OperationalFields = map[string]json.RawMessage{"kling_tariff_contract": raw}
		}
		return out
	}
	if !reflect.DeepEqual(withoutColumns(legacy.Entries, "Price"), withoutColumns(latest.Entries, "Output Price")) || len(legacyRows) != 9 {
		t.Fatal("legacy observations differ, source rows:", len(legacyRows))
	}
	var native catalogsync.Capture
	a := &Adapter{}
	for _, entry := range latest.Entries {
		model, ok := pricingModel(entry.ID)
		if !ok {
			t.Fatal("unreviewed executable tariff", entry.ID)
		}
		native.Models = append(native.Models, model)
		native.IDs = append(native.IDs, model.ID)
		for _, rate := range entry.Rates {
			r, err := a.CatalogRateRequest(model, rate)
			if err != nil {
				t.Fatal(err)
			}
			cost, err := pricingQuote(a, r)
			if err != nil || cost.Pricing.Unit != rate.Unit || cost.Pricing.Price != rate.Price {
				t.Fatal(entry.ID, cost, rate, err)
			}
			if entry.ID == ImageMultiShot {
				for _, n := range []float64{1, 4, 9} {
					if rate.Unit != "request" || cost.ExpectedUSD != 70000 || !cost.MeterUsage(core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "image_count", Unit: "image", Quantity: n}}}) || *cost.MeteredUSD != 70000 {
						t.Fatal("Multi-Shot must bill one call regardless of returned views", n, cost, rate)
					}
				}
			}
		}
	}
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	check := func(model core.MediaModel, rate catalogsync.Rate) error {
		_, err := a.CatalogRateRequest(model, rate)
		return err
	}
	previous, _ := catalogsync.Reconcile(catalogsync.State{}, native, []catalogsync.Observation{latest}, now, check)
	if len(previous.Models[ImageMultiShot].Rates) != 1 {
		t.Fatal("setup did not confirm the call tariff")
	}
	multiShotLine := ""
	for _, line := range strings.Split(current, "\n") {
		if strings.HasPrefix(line, "| General | AI Multi-Shot |") {
			multiShotLine = line
			break
		}
	}
	if multiShotLine == "" {
		t.Fatal("fixture missing Multi-Shot row")
	}
	for _, tc := range []struct{ name, old, new string }{
		{"legacy row missing", multiShotLine, ""},
		{"legacy row duplicated", multiShotLine, multiShotLine + "\n" + multiShotLine},
		{"unsupported input term", image4InputPrice, "0.8 Units ($0.003) / image"},
		{"legacy method", "Billed per call", "Billed per image"},
		{"legacy suffix", "/ call", "/ image"},
		{"legacy input", "Billed per call | - | -", "Billed per call | 1 Unit ($0.01) / image | -"},
		{"legacy length", "Billed per call | - | -", "Billed per call | - | Short"},
		{"unknown model", "Kling Image 3.0", "Future Image"},
		{"unsupported selector", "Image Series (Text-to-Image, Image-to-Image)", "Image Set (Text-to-Image, Image-to-Image)"},
		{"unsupported formula", "| " + image4InputOutputBilling + " |", "| Total cost = Reference images |"},
		{"unsupported output unit", "16 Units ($0.063) / image", "16 Units ($0.063) / call"},
		{"unexpected chart", "Output Specs - Length", "Output Specs - Width"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := strings.Replace(current, tc.old, tc.new, 1)
			if mutated == current {
				t.Fatal("test mutation did not change fixture")
			}
			ob, err := observe(mutated)
			if err == nil || len(ob.Entries) != 0 {
				t.Fatal("unexpected partial interpretation", err)
			}
			ob.Error = "Operational source interpretation failed; last confirmed information was preserved"
			ob.FailureStage, ob.FetchedHash, ob.Hash = "interpretation", ob.Hash, ""
			next, run := catalogsync.Reconcile(previous, native, []catalogsync.Observation{ob}, now.Add(time.Minute), check)
			if len(run.OpenWarnings) == 0 {
				t.Fatal("interpretation failure did not produce a warning")
			}
			for id, e := range previous.Models {
				if !reflect.DeepEqual(next.Models[id].Rates, e.Rates) {
					t.Fatal("drift discarded confirmed rates", id)
				}
			}
		})
	}
}
