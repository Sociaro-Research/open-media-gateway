package kling

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func pricingFixture(t *testing.T, name string) (catalogsync.Source, string) {
	t.Helper()
	url := priceSource
	switch name {
	case "image":
		url = imagePriceSource
	case "ecommerce":
		url = ecommercePriceSource
	case "effects":
		url = effectsPriceSource
	}
	file := "testdata/pricing.md"
	if name != "video" {
		file = "testdata/pricing_" + name + ".md"
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return catalogsync.Source{ID: "pricing-" + name, URL: url + ".md", Parser: "document"}, string(raw)
}
func pricingEntries(t *testing.T, name string) map[string]catalogsync.Entry {
	t.Helper()
	source, text := pricingFixture(t, name)
	entries, err := interpretPrices(source, text)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]catalogsync.Entry{}
	for _, e := range entries {
		if _, exists := out[e.ID]; exists {
			t.Fatalf("duplicate model row %s", e.ID)
		}
		out[e.ID] = e
	}
	return out
}
func pricingQuote(a *Adapter, r core.MediaRequest) (core.TaskCost, error) {
	var cost core.TaskCost
	var err error
	switch {
	case isImageOperation(r.Model):
		cost, _, err = a.quoteImage(r)
	case isMotionOperation(r.Model):
		cost, _, err = a.quoteMotion(r)
	case isAudioOperation(r.Model):
		cost, _, err = a.quoteAudio(r)
	case isLegacyOmniOperation(r.Model):
		cost, _, err = a.quoteLegacyOmni(r)
	case isSolutionOperation(r.Model):
		cost, _, err = quoteSolution(r)
	case isResourceGenerationOperation(r.Model):
		cost, _, err = a.quoteResourceGeneration(pricingProbeContext(), r)
	case isAuxiliaryOperation(r.Model):
		cost, _, err = a.quoteAuxiliary(r)
	default:
		cost, _, err = a.Quote(pricingProbeContext(), core.MediaModel{}, r)
	}
	return cost, err
}

func TestPublishedPricingEveryActiveTariffMatchesValidatedAdapterQuote(t *testing.T) {
	a := adapter(t, transport(func(*http.Request) (*http.Response, error) {
		t.Fatal("catalogue probes must not perform HTTP or generate resources")
		return nil, nil
	}))
	exercised := map[string]bool{}
	for _, doc := range []string{"video", "image", "ecommerce", "effects"} {
		for _, entry := range pricingEntries(t, doc) {
			m, ok := pricingModel(entry.ID)
			if !ok {
				t.Fatalf("price source names an unknown operation %s", entry.ID)
			}
			selectors := map[string]bool{}
			for i, rate := range entry.Rates {
				t.Run(fmt.Sprintf("%s/%s/%d", doc, entry.ID, i), func(t *testing.T) {
					key := klingRateKey(rate)
					if selectors[key] {
						t.Fatal("duplicate rate selectors", rate)
					}
					selectors[key] = true
					if rate.Source == "" || rate.Region != "Singapore" || rate.Price <= 0 || rate.Per != 1 {
						t.Fatal(rate)
					}
					r, err := a.CatalogRateRequest(m, rate)
					if err != nil {
						t.Fatal(err)
					}
					if r.Model != entry.ID || !rate.MatchesConditions(m, r) {
						t.Fatal("probe mismatches exact operation/selectors", r)
					}
					cost, err := pricingQuote(a, r)
					if err != nil {
						t.Fatal(err)
					}
					if cost.Pricing.Price != rate.Price || cost.Pricing.Unit != rate.Unit || cost.Pricing.Per != rate.Per || cost.Pricing.DurationBillingRule != rate.DurationBillingRule {
						t.Fatalf("source vs adapter tariff: %#v; %#v", rate, cost)
					}
					repriced, err := catalogsync.Reprice(cost, rate)
					if err != nil || repriced.ExpectedUSD != cost.ExpectedUSD || repriced.QuantityUnknown != cost.QuantityUnknown {
						t.Fatal(repriced, err)
					}
					if cost.QuantityUnknown {
						raw, _ := json.Marshal(cost)
						if strings.Contains(string(raw), "expected_usd") || strings.Contains(string(raw), "estimated_units") {
							t.Fatalf("unknown quantity became zero/free: %s", raw)
						}
					} else if cost.ExpectedUSD <= 0 || cost.EstimatedUnits <= 0 {
						t.Fatal("paid known-quantity tariff became free", cost)
					}
					exercised[entry.ID] = true
				})
			}
		}
	}
	expected := []string{TextToVideo, ImageToVideo, TextToVideo4, ImageToVideo4, OmniVideo4, TextToVideo3Turbo, ImageToVideo3Turbo, TextToVideo25Turbo, ImageToVideo25Turbo, TextToVideo26, ImageToVideo26, MotionControl3, MotionControl26, OmniVideo3, OmniVideoO1, TextToAudio, VideoToAudio, TextToSpeech, ImageRecognition, FaceDetection, ResourceCreateVoice, ImageGeneration3, ImageGeneration21, ImageOmni3, ImageO1, MultiImage21, ImageOutpainting, ImageMultiShot, ApparelReplicator, GoodsStudio, VideoCommerce, VirtualTryOn}
	for _, id := range expected {
		if !exercised[id] {
			t.Fatalf("documented executable pricing path was not checked: %s", id)
		}
	}
}

func TestPublishedPricingGoldenConditionalPrices(t *testing.T) {
	// Explicit expected USD values and selectors, independently transcribed from
	// the four official tables. These do not derive expected values from helpers.
	type golden struct {
		doc, id, res, parameterAudio    string
		audio, video, image, voice, bgm *bool
		resourceMin                     *int
		roleMin                         *int
		want                            money.USD
		unit                            string
	}
	pInt := func(n int) *int { return &n }
	rows := []golden{
		{doc: "video", id: TextToVideo, res: "720p", audio: priceBool(false), want: 84000, unit: "second"},
		{doc: "video", id: ImageToVideo, res: "1080p", audio: priceBool(true), want: 168000, unit: "second"},
		{doc: "video", id: TextToVideo4, res: "1080p", want: 235000, unit: "second"},
		{doc: "video", id: OmniVideo4, res: "720p", video: priceBool(true), want: 94000, unit: "second"},
		{doc: "video", id: OmniVideo4, res: "1080p", video: priceBool(true), want: 141000, unit: "second"},
		{doc: "video", id: TextToVideo3Turbo, res: "720p", want: 112000, unit: "second"},
		{doc: "video", id: ImageToVideo25Turbo, res: "1080p", want: 70000, unit: "second"},
		{doc: "video", id: TextToVideo26, res: "720p", audio: priceBool(false), want: 42000, unit: "second"},
		{doc: "video", id: ImageToVideo26, res: "1080p", audio: priceBool(true), resourceMin: pInt(0), want: 140000, unit: "second"},
		{doc: "video", id: ImageToVideo26, res: "1080p", audio: priceBool(true), resourceMin: pInt(1), want: 168000, unit: "second"},
		{doc: "video", id: MotionControl26, res: "1080p", want: 112000, unit: "second"},
		{doc: "video", id: MotionControl3, res: "720p", want: 126000, unit: "second"},
		{doc: "video", id: OmniVideo3, res: "720p", parameterAudio: "native", want: 112000, unit: "second"},
		{doc: "video", id: OmniVideo3, res: "1080p", parameterAudio: "original", video: priceBool(true), want: 168000, unit: "second"},
		{doc: "video", id: OmniVideoO1, res: "720p", video: priceBool(true), want: 126000, unit: "second"},
		{doc: "video", id: TextToAudio, want: 35000, unit: "request"}, {doc: "video", id: VideoToAudio, want: 35000, unit: "request"}, {doc: "video", id: TextToSpeech, want: 7000, unit: "request"},
		{doc: "video", id: ResourceCreateVoice, want: 7000, unit: "request"}, {doc: "video", id: ImageRecognition, want: 14000, unit: "request"}, {doc: "video", id: FaceDetection, want: 7000, unit: "request"},
		{doc: "image", id: ImageGeneration3, res: "2k", want: 28000, unit: "image"}, {doc: "image", id: ImageOmni3, res: "4k", want: 56000, unit: "image"}, {doc: "image", id: ImageO1, res: "1k", want: 28000, unit: "image"},
		{doc: "image", id: ImageGeneration21, res: "1k", image: priceBool(false), want: 14000, unit: "image"}, {doc: "image", id: ImageGeneration21, res: "1k", image: priceBool(true), want: 28000, unit: "image"},
		{doc: "image", id: MultiImage21, want: 56000, unit: "image"}, {doc: "image", id: ImageOutpainting, want: 28000, unit: "image"}, {doc: "image", id: ImageMultiShot, want: 70000, unit: "request"},
		{doc: "ecommerce", id: ApparelReplicator, res: "720p", voice: priceBool(false), bgm: priceBool(false), want: 182000, unit: "second"}, {doc: "ecommerce", id: ApparelReplicator, res: "1080p", voice: priceBool(true), bgm: priceBool(false), want: 280000, unit: "second"},
		{doc: "ecommerce", id: GoodsStudio, res: "1080p", want: 280000, unit: "second"}, {doc: "ecommerce", id: VideoCommerce, res: "720p", roleMin: pInt(0), want: 140000, unit: "second"}, {doc: "ecommerce", id: VideoCommerce, res: "720p", roleMin: pInt(1), want: 196000, unit: "second"}, {doc: "ecommerce", id: VirtualTryOn, want: 112000, unit: "image"},
	}
	for _, g := range rows {
		t.Run(fmt.Sprint(g.doc, "/", g.id, "/", g.res, "/", g.want), func(t *testing.T) {
			entry := pricingEntries(t, g.doc)[g.id]
			matches := 0
			for _, r := range entry.Rates {
				if r.Resolution != g.res || g.audio != nil && (r.Audio == nil || *r.Audio != *g.audio) || g.video != nil && (r.VideoInput == nil || *r.VideoInput != *g.video) || g.image != nil && (r.ImageInput == nil || *r.ImageInput != *g.image) || g.voice != nil && (r.VoiceInput == nil || *r.VoiceInput != *g.voice) || g.parameterAudio != "" && r.Parameters["audio"] != g.parameterAudio {
					continue
				}
				if g.bgm != nil && (r.AudioInput == nil || *r.AudioInput != *g.bgm) {
					continue
				}
				if g.resourceMin != nil && (len(r.ResourceInputs) != 1 || r.ResourceInputs[0].Min != *g.resourceMin) {
					continue
				}
				if g.roleMin != nil && (len(r.ReferenceRoles) != 1 || r.ReferenceRoles[0].Min != *g.roleMin) {
					continue
				}
				matches++
				if r.Price != g.want || r.Unit != g.unit || r.Per != 1 {
					t.Fatal(r)
				}
				if g.id == OmniVideo4 && (!r.VideoInputIncludesResources || g.video != nil && *g.video && r.DurationBillingRule != "output_plus_max_input_output") {
					t.Fatal("lost resource-inclusive Kling4 formula", r)
				}
			}
			if matches != 1 {
				t.Fatalf("got %d matching exact selectors: %#v", matches, entry.Rates)
			}
		})
	}
}

func TestPublishedImageQuantityRulesAndConditionIsolation(t *testing.T) {
	a := &Adapter{}
	entries := pricingEntries(t, "image")
	for _, id := range []string{ImageGeneration3, ImageGeneration21, ImageOmni3, ImageO1, MultiImage21, ImageOutpainting, ImageMultiShot} {
		m, _ := pricingModel(id)
		rate := entries[id].Rates[0]
		r, err := a.CatalogRateRequest(m, rate)
		if err != nil {
			t.Fatal(err)
		}
		if id != ImageMultiShot {
			r.Options.OutputCount = 3
		}
		cost, err := pricingQuote(a, r)
		if err != nil {
			t.Fatal(err)
		}
		quantity := 3.0
		if id == ImageMultiShot {
			quantity = 1
		}
		want, _ := rate.Price.Multiply(quantity, 1)
		if cost.ExpectedUSD != want || cost.EstimatedUnits != quantity {
			t.Fatal(id, cost)
		}
	}
	r := core.MediaRequest{Model: ImageOmni3, Prompt: "Synthetic series", Options: core.MediaOptions{Ratio: "16:9", Resolution: "4k", ImageURLs: []string{"https://cdn.example/series.png"}, Parameters: map[string]any{"result_type": "series", "series_amount": "4"}}}
	cost, err := pricingQuote(a, r)
	if err != nil || cost.ExpectedUSD != 224000 || cost.EstimatedUnits != 4 {
		t.Fatal(cost, err)
	}
	r.Options.Parameters["series_amount"] = "auto"
	if _, err := pricingQuote(a, r); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("automatic series count guessed", err)
	}
	// I2I changes the price of every OUTPUT; it is not a one-time input fee.
	i21 := entries[ImageGeneration21]
	if len(i21.Rates) != 4 {
		t.Fatal(i21)
	}
	for _, rate := range i21.Rates {
		if rate.InputImages != nil || rate.ImageInput == nil {
			t.Fatal("invented input-image surcharge", rate)
		}
	}
}

func TestPublishedUnknownTariffsRemainWarnings(t *testing.T) {
	for _, doc := range []string{"video", "effects"} {
		entries := pricingEntries(t, doc)
		ids := []string{AvatarImageToVideo, LipSync, MultiElementsEditing, ResourceCreateElement}
		if doc == "effects" {
			ids = []string{KlingEffects}
		}
		for _, id := range ids {
			e := entries[id]
			if e.ID != id || len(e.Rates) != 0 || e.Metadata == nil || len(e.Metadata.Facts) == 0 || e.Metadata.Facts[0].Label != "Published tariff condition" {
				t.Fatal("unknown tariff became active/free", id, e)
			}
		}
	}
}

func TestPublishedPricingRejectsDriftAndKeepsCurrencySeparate(t *testing.T) {
	source, text := pricingFixture(t, "video")
	baseline, err := interpretPrices(source, text)
	if err != nil {
		t.Fatal(err)
	}
	unitChange := strings.Replace(text, "1.5 Units ($0.235)", "999 Units ($0.235)", 1)
	changed, err := interpretPrices(source, unitChange)
	if err != nil {
		t.Fatal(err)
	}
	rates := func(entries []catalogsync.Entry) map[string][]catalogsync.Rate {
		m := map[string][]catalogsync.Rate{}
		for _, e := range entries {
			m[e.ID] = e.Rates
		}
		return m
	}
	if !reflect.DeepEqual(rates(baseline), rates(changed)) {
		t.Fatal("package unit change converted USD")
	}
	foundUnits := false
	for _, e := range changed {
		var c []klingPublishedTariff
		_ = json.Unmarshal(e.OperationalFields["kling_tariff_contract"], &c)
		for _, r := range c {
			foundUnits = foundUnits || r.PackageUnits == "999"
		}
	}
	if !foundUnits {
		t.Fatal("separate package-unit evidence discarded")
	}
	updated, err := interpretPrices(source, strings.Replace(text, "($0.235)", "($0.240)", 1))
	if err != nil {
		t.Fatal(err)
	}
	var changedRate catalogsync.Rate
	for _, e := range updated {
		if e.ID == TextToVideo4 {
			for _, r := range e.Rates {
				if r.Resolution == "1080p" {
					changedRate = r
				}
			}
		}
	}
	m, _ := pricingModel(TextToVideo4)
	probe, err := new(Adapter).CatalogRateRequest(m, changedRate)
	if err != nil {
		t.Fatal(err)
	}
	base, err := pricingQuote(&Adapter{}, probe)
	if err != nil {
		t.Fatal(err)
	}
	repriced, err := catalogsync.Reprice(base, changedRate)
	if err != nil || repriced.ExpectedUSD != 2400000 {
		t.Fatal(repriced, err)
	}
	cases := map[string]string{
		"HTML shell": "<html>JavaScript required</html>", "wrong currency": strings.Replace(text, "($0.235)", "(CNY0.235)", 1), "unit only": strings.Replace(text, " ($0.235)", "", 1),
		"missing reviewed row": strings.Replace(text, "| Kling 3.0 | No Native Audio", "| Unknown | No Native Audio", 1), "changed header": strings.Replace(text, "Price (720P)", "Price (480P)", 1),
		"changed billing": strings.Replace(text, "Total duration = Output duration)", "Total duration = Input duration)", 1), "preview": text + "\nPreview prices", "locale": strings.Replace(text, "> Locale: en", "> Locale: zh", 1),
		"input branches diverge": strings.Replace(text, "($0.094)", "($0.095)", 1), "added 4k": strings.Replace(text, "1.5 Units ($0.235) /s | - |", "1.5 Units ($0.235) /s | 2 Units ($0.3) /s |", 1),
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "| Kling 3.0 | No Native Audio") {
			cases["duplicate row"] = text + "\n" + line
			break
		}
	}
	cases["locale prefix"] = strings.Replace(text, "> Locale: en", "> Locale: english", 1)
	cases["duplicate provenance"] = text + "\n> Source: " + priceSource + "\n"
	cases["duplicate locale"] = text + "\n> Locale: en\n"
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := interpretPrices(source, value); err == nil {
				t.Fatal("unreviewed source drift accepted")
			}
		})
	}
	for _, doc := range []string{"image", "ecommerce", "effects"} {
		s, v := pricingFixture(t, doc)
		if _, err := interpretPrices(s, strings.Replace(v, "> Locale: en", "> Locale: zh", 1)); err == nil {
			t.Fatal("untrusted locale accepted", doc)
		}
	}
	effectsSource, effectsText := pricingFixture(t, "effects")
	if _, err := interpretPrices(effectsSource, effectsText+"\n| New effect | 720p | 1 Unit ($0.14) /s |\n"); err == nil {
		t.Fatal("new Effects numeric row silently interpreted as no price")
	}
	if entries, err := interpretPrices(catalogsync.Source{URL: "https://other.example/prices.md"}, text); err != nil || len(entries) > 0 {
		t.Fatal("untrusted source parsed")
	}
}

func TestCatalogRateRequestRejectsUnreviewedSelectors(t *testing.T) {
	m, _ := pricingModel(OmniVideo3)
	for _, rate := range []catalogsync.Rate{
		{Region: "unreviewed"}, {Region: "Singapore", Parameters: map[string]string{"raw_json": "{}"}},
		{Region: "Singapore", ResourceInputs: []catalogsync.ResourceCountSelector{{Kind: "unreviewed", Min: 1, Max: 1}}},
		{Region: "Singapore", Quality: "made-up"}, {Region: "Singapore", InputImages: &catalogsync.ImageInputTariff{Price: 1}},
	} {
		if _, err := new(Adapter).CatalogRateRequest(m, rate); err == nil {
			t.Fatal("unreviewed selector accepted", rate)
		}
	}
	if _, err := new(Adapter).CatalogRateRequest(core.MediaModel{ID: "kling-unreviewed"}, catalogsync.Rate{}); !errors.Is(err, core.ErrModelUnavailable) {
		t.Fatal(err)
	}
}

func TestPublishedSelectorIsolationAndDeduplication(t *testing.T) {
	entries := pricingEntries(t, "video")
	a := &Adapter{}
	for _, id := range []string{ImageToVideo26, OmniVideo3, OmniVideo4} {
		m, _ := pricingModel(id)
		for _, selected := range entries[id].Rates {
			r, err := a.CatalogRateRequest(m, selected)
			if err != nil {
				t.Fatal(err)
			}
			matches := 0
			for _, candidate := range entries[id].Rates {
				if candidate.Resolution != selected.Resolution || !candidate.MatchesConditions(m, r) {
					continue
				}
				hasVideo := len(r.Options.Videos()) > 0
				if candidate.VideoInputIncludesResources {
					for _, resource := range r.Options.ResourceSnapshots {
						hasVideo = hasVideo || resource.Kind == "element" && resource.Subtype == "video_character"
					}
				}
				if candidate.VideoInput != nil && *candidate.VideoInput != hasVideo {
					continue
				}
				if candidate.Audio != nil && (r.Options.GenerateAudio == nil || *candidate.Audio != *r.Options.GenerateAudio) {
					continue
				}
				matches++
			}
			if matches != 1 {
				t.Fatalf("%s has %d overlapping selectors for %#v", id, matches, r.Options)
			}
		}
	}
	r := catalogsync.Rate{Region: "Singapore", Parameters: map[string]string{"audio": "off"}, ResourceInputs: []catalogsync.ResourceCountSelector{{Kind: "voice", Min: 0, Max: 0}, {Kind: "element", Subtype: "video_character", Min: 0, Max: 0}}}
	reordered := r
	reordered.ResourceInputs = []catalogsync.ResourceCountSelector{r.ResourceInputs[1], r.ResourceInputs[0]}
	if klingRateKey(r) != klingRateKey(reordered) {
		t.Fatal("selector ordering created duplicate tariff")
	}
	reordered.Duration = 10
	if klingRateKey(r) == klingRateKey(reordered) {
		t.Fatal("exact-duration selector disappeared")
	}
	for _, rate := range []catalogsync.Rate{
		{Region: "Singapore", ResourceInputs: []catalogsync.ResourceCountSelector{{Kind: "element", Subtype: "invented", Min: 0, Max: 0}}},
		{Region: "Singapore", ReferenceRoles: []catalogsync.RoleCountSelector{{Kind: "image", Role: "invented", Min: 0, Max: 0}}},
	} {
		m, _ := pricingModel(OmniVideo3)
		if _, err := a.CatalogRateRequest(m, rate); err == nil {
			t.Fatal("zero-count unknown selector silently accepted", rate)
		}
	}
}

func TestPublishedOmni4ResourceOnlyInputSelector(t *testing.T) {
	entry := pricingEntries(t, "video")[OmniVideo4]
	m := omni4Model()
	a := &Adapter{}
	r := omni4ResourceFixture()
	omni4AddResource(&r, "element", "video_character", ptr(17.0))
	matched := 0
	for _, rate := range entry.Rates {
		if rate.Resolution != "1080p" || rate.VideoInput == nil || !*rate.VideoInput || !rate.VideoInputIncludesResources || !rate.MatchesConditions(m, r) {
			continue
		}
		base, _, err := a.quoteOmni4(m, r)
		if err != nil {
			t.Fatal(err)
		}
		cost, err := catalogsync.Reprice(base, rate)
		if err != nil || cost.ExpectedUSD != 3807000 {
			t.Fatal("resource-only input mispriced", cost, err)
		}
		matched++
	}
	if matched != 1 {
		t.Fatal("resource-only input lost its conditional rate", matched)
	}
}

func TestPublishedRateUpdateKeepsUnknownQuantityUnpriced(t *testing.T) {
	source, text := pricingFixture(t, "ecommerce")
	text = strings.Replace(text, "($0.182)", "($0.183)", 1)
	entries, err := interpretPrices(source, text)
	if err != nil {
		t.Fatal(err)
	}
	a := &Adapter{}
	m, _ := pricingModel(ApparelReplicator)
	checked := false
	for _, entry := range entries {
		if entry.ID != ApparelReplicator {
			continue
		}
		for _, rate := range entry.Rates {
			if rate.Resolution != "720p" || rate.VoiceInput == nil || *rate.VoiceInput {
				continue
			}
			r, err := a.CatalogRateRequest(m, rate)
			if err != nil {
				t.Fatal(err)
			}
			base, err := pricingQuote(a, r)
			if err != nil {
				t.Fatal(err)
			}
			cost, err := catalogsync.Reprice(base, rate)
			if err != nil || !cost.QuantityUnknown || cost.Status != "quantity_unknown" || cost.Pricing.Price != 183000 {
				t.Fatal(cost, err)
			}
			raw, _ := json.Marshal(cost)
			if strings.Contains(string(raw), "expected_usd") || strings.Contains(string(raw), "estimated_units") || strings.Contains(cost.Pricing.EstimateDescription, "0 planning") || strings.Contains(cost.Pricing.EstimateDescription, " = $0") {
				t.Fatalf("published rate update manufactured a zero total: %s", raw)
			}
			checked = true
		}
	}
	if !checked {
		t.Fatal("updated conditional tariff missing")
	}
}
