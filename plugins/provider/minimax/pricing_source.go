package minimax

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"golang.org/x/net/html"
)

var priceTable = regexp.MustCompile(`(?s)<table>.*?</table>`)
var h3Model = regexp.MustCompile(`MiniMax-[A-Za-z0-9._-]+`)
var dollarAmount = regexp.MustCompile(`^\$([0-9]+(?:\.[0-9]+)?)$`)
var legacyPrice = regexp.MustCompile(`\|\s*(MiniMax-Hailuo-(?:2\.3-Fast|2\.3|02))\s*\|\s*\\?\$([0-9]+(?:\.[0-9]+)?) per (512|768|1080)P, (6|10)s video\s*\|`)
var imagePrice = regexp.MustCompile(`\|\s*image-01\s*\|\s*\\?\$([0-9]+(?:\.[0-9]+)?) per image\s*\|`)
var regenerationPrice = regexp.MustCompile(`\|\s*MiniMax-H3-Regeneration\s*\|\s*768P → 2K\s*\|\s*\\?\$([0-9]+(?:\.[0-9]+)?)\s*\|\s*\\?\$([0-9]+(?:\.[0-9]+)?)\s*\|\s*\\?\$([0-9]+(?:\.[0-9]+)?)\s*\|`)
var contextPrice = regexp.MustCompile(`\|\s*MiniMax-H3-Context-IR\s*\|\s*\\?\$([0-9]+(?:\.[0-9]+)?)\s*\|\s*\\?\$([0-9]+(?:\.[0-9]+)?)\s*\|`)
var regenerationPriceHeader = regexp.MustCompile(`\|\s*Model / API\s*\|\s*Spec\s*\|\s*Output Video\s+per second\s*\|\s*Input Video\s+per second\s*\|\s*Input Image\s+per image beyond free quota\s*\|\s*(?:\|[\s:\-]+\|[\s:\-]+\|[\s:\-]+\|[\s:\-]+\|[\s:\-]+\|\s*)?\|\s*MiniMax-H3-Regeneration\s*\|`)
var contextPriceHeader = regexp.MustCompile(`\|\s*Model / API\s*\|\s*Input Price\s+per M tokens\s*\|\s*Output Price\s+per M tokens\s*\|\s*(?:\|[\s:\-]+\|[\s:\-]+\|[\s:\-]+\|\s*)?\|\s*MiniMax-H3-Context-IR\s*\|`)

// interpretPrices recognizes the reviewed public table shape and its three
// separate billable metrics. Changed units/quotas or incomplete rows fail closed;
// catalogue reconciliation then preserves the last confirmed rates.
func interpretPrices(source catalogsync.Source, text string) ([]catalogsync.Entry, error) {
	if source.URL != priceSource && source.URL != priceSource+".md" {
		return nil, nil
	}
	if len(text) > 2<<20 {
		return nil, errors.New("MiniMax price document exceeds reviewed size")
	}
	if !strings.Contains(text, "first **5 images** per task are free") || !strings.Contains(text, "first **2 images** per task are free") || strings.Count(text, "Audio input is free") != 2 {
		return nil, errors.New("MiniMax image allowances or free-audio rule changed; retain last confirmed tariffs")
	}
	entries := map[string]catalogsync.Entry{}
	seen := map[string]bool{}
	tables, e := pricingTables(text)
	if e != nil {
		return nil, e
	}
	for _, rows := range tables {
		if len(rows) == 0 || len(rows[0]) != 5 {
			continue
		}
		expected := []string{"Model", "Output Resolution", "Output Video per second", "Input Video per second", "Input Image per image beyond free quota"}
		if strings.Join(rows[0], "|") != strings.Join(expected, "|") {
			continue
		}
		model := ""
		for _, cells := range rows[1:] {
			if len(cells) == 5 {
				models := h3Model.FindAllString(cells[0], -1)
				if len(models) != 1 {
					return nil, errors.New("MiniMax price row has an ambiguous model label")
				}
				model = models[0]
				cells = cells[1:]
			}
			if len(cells) != 4 || model == "" {
				return nil, errors.New("MiniMax price row shape changed")
			}
			m, ok := modelFor(model)
			if !ok {
				return nil, errors.New("MiniMax price row names an unreviewed model")
			}
			res := strings.ToLower(cells[0])
			if !containsString(m.Resolutions, res) {
				return nil, errors.New("MiniMax price row introduces an unreviewed resolution")
			}
			key := model + "/" + res
			if seen[key] {
				return nil, errors.New("MiniMax price rows are duplicated")
			}
			seen[key] = true
			prices := []money.USD{}
			for _, cell := range cells[1:] {
				match := dollarAmount.FindStringSubmatch(strings.ReplaceAll(cell, `\$`, `$`))
				if len(match) != 2 {
					return nil, errors.New("MiniMax price or USD unit is missing")
				}
				p, e := money.Parse(match[1])
				if e != nil || p <= 0 {
					return nil, errors.New("MiniMax price is invalid")
				}
				prices = append(prices, p)
			}
			entry := entries[model]
			entry.ID = model
			for _, video := range []bool{false, true} {
				for _, audio := range []bool{false, true} {
					v, au := video, audio
					entry.Rates = append(entry.Rates, catalogsync.Rate{Region: "Global", Resolution: res, VideoInput: &v, AudioInput: &au, Unit: "second", Per: 1, Price: prices[0], Source: source.URL, Components: []core.PricingComponent{{Metric: "output_video_duration", Price: prices[0], Per: 1}, {Metric: "input_video_duration", Price: prices[1], Per: 1}, {Metric: "billable_input_images", Price: prices[2], Per: 1}}})
				}
			}
			entries[model] = entry
		}
	}
	if len(seen) != 8 {
		return nil, errors.New("MiniMax H3 price rows are incomplete; retain last confirmed tariffs")
	}
	match := imagePrice.FindAllStringSubmatch(text, -1)
	if len(match) != 1 {
		return nil, errors.New("MiniMax image-01 price is missing or ambiguous")
	}
	amount, e := money.Parse(match[0][1])
	if e != nil || amount <= 0 {
		return nil, errors.New("MiniMax image price is invalid")
	}
	entries["image-01"] = catalogsync.Entry{ID: "image-01", Rates: []catalogsync.Rate{{Region: "Global", Unit: "image", Per: 1, Price: amount, Source: source.URL}}}
	legacySeen := map[string]bool{}
	for _, row := range legacyPrice.FindAllStringSubmatch(text, -1) {
		id := row[1]
		res := row[3] + "p"
		dur, _ := strconv.Atoi(row[4])
		key := id + "/" + res + "/" + row[4]
		if legacySeen[key] {
			return nil, errors.New("MiniMax legacy duration price rows are duplicated")
		}
		legacySeen[key] = true
		m, ok := modelFor(id)
		if !ok || !containsString(m.Resolutions, res) || res == "1080p" && dur != 6 {
			return nil, errors.New("MiniMax legacy price has unsupported selectors")
		}
		amount, e := money.Parse(row[2])
		if e != nil || amount <= 0 {
			return nil, errors.New("MiniMax legacy price is invalid")
		}
		entry := entries[id]
		entry.ID = id
		entry.Rates = append(entry.Rates, catalogsync.Rate{Region: "Global", Resolution: res, Duration: dur, Unit: "request", Per: 1, Price: amount, Source: source.URL})
		entries[id] = entry
	}
	if len(legacySeen) != 11 {
		return nil, errors.New("MiniMax legacy duration price rows are incomplete; retain last confirmed tariffs")
	}
	clean := strings.Join(strings.Fields(strings.ReplaceAll(text, "*", "")), " ")
	regeneration := regenerationPrice.FindAllStringSubmatch(text, -1)
	contextRows := contextPrice.FindAllStringSubmatch(text, -1)
	if len(regeneration) != 1 || len(contextRows) != 1 || !regenerationPriceHeader.MatchString(strings.ReplaceAll(text, "*", "")) || !contextPriceHeader.MatchString(strings.ReplaceAll(text, "*", "")) || !strings.Contains(clean, "Input materials from the original task are billed again") || !strings.Contains(clean, "first 5 images are free") || !strings.Contains(clean, "audio is free") {
		return nil, errors.New("MiniMax context/regeneration prices, units or recharge rule changed; retain last confirmed tariffs")
	}
	parsePrices := func(cells []string) ([]money.USD, error) {
		values := []money.USD{}
		for _, cell := range cells {
			value, err := money.Parse(cell)
			if err != nil || value <= 0 {
				return nil, errors.New("invalid MiniMax context/regeneration tariff")
			}
			values = append(values, value)
		}
		return values, nil
	}
	regenerationValues, err := parsePrices(regeneration[0][1:])
	if err != nil {
		return nil, err
	}
	contextValues, err := parsePrices(contextRows[0][1:])
	if err != nil {
		return nil, err
	}
	regenerationRate := func() catalogsync.Rate {
		return catalogsync.Rate{Region: "Global", Unit: "second", Per: 1, Price: regenerationValues[0], Source: source.URL, Components: []core.PricingComponent{{Metric: "output_video_duration", Price: regenerationValues[0], Per: 1}, {Metric: "input_video_duration", Price: regenerationValues[1], Per: 1}, {Metric: "billable_input_images", Price: regenerationValues[2], Per: 1}}}
	}
	entries[Regeneration] = catalogsync.Entry{ID: Regeneration, Rates: []catalogsync.Rate{regenerationRate()}}
	baseEntry := catalogsync.Entry{ID: RegenerationVideo}
	for _, audio := range []bool{false, true} {
		video, au := true, audio
		rate := regenerationRate()
		rate.VideoInput = &video
		rate.AudioInput = &au
		baseEntry.Rates = append(baseEntry.Rates, rate)
	}
	entries[RegenerationVideo] = baseEntry
	contextEntry := catalogsync.Entry{ID: ContextIR}
	for _, video := range []bool{false, true} {
		for _, audio := range []bool{false, true} {
			v, au := video, audio
			contextEntry.Rates = append(contextEntry.Rates, catalogsync.Rate{Region: "Global", Unit: "token", Per: 1_000_000, VideoInput: &v, AudioInput: &au, Source: source.URL, Components: []core.PricingComponent{{Metric: "prompt_tokens", Price: contextValues[0], Per: 1_000_000}, {Metric: "completion_tokens", Price: contextValues[1], Per: 1_000_000}}})
		}
	}
	entries[ContextIR] = contextEntry
	out := []catalogsync.Entry{}
	for _, id := range []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max", "image-01", "MiniMax-Hailuo-2.3", "MiniMax-Hailuo-2.3-Fast", "MiniMax-Hailuo-02", ContextIR, Regeneration, RegenerationVideo} {
		out = append(out, entries[id])
	}
	return out, nil
}
func containsString(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}
func tableRows(table string) ([][]string, error) {
	root, e := html.Parse(strings.NewReader(table))
	if e != nil {
		return nil, e
	}
	var rows [][]string
	var visit func(*html.Node)
	var content func(*html.Node) string
	content = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		s := ""
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			s += content(c) + " "
		}
		return s
	}
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			cells := []string{}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.Data == "th" || c.Data == "td") {
					v := strings.ReplaceAll(content(c), "\\", "")
					cells = append(cells, strings.Join(strings.Fields(v), " "))
				}
			}
			rows = append(rows, cells)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(root)
	return rows, nil
}

// CatalogRateRequest supplies a representative executable workflow for an exact
// reviewed tariff. In particular Hailuo-02 512P belongs to first-frame input;
// a text-only sample cannot establish that tariff's supported estimator.
func (a *Adapter) CatalogRateRequest(model core.MediaModel, rate catalogsync.Rate) (core.MediaRequest, error) {
	m, ok := modelFor(model.ID)
	if !ok {
		return core.MediaRequest{}, invalid("unreviewed model for catalogue tariff")
	}
	if m.MediaType == "resource" || m.ResourceOperation != nil {
		return core.MediaRequest{}, invalid("resource management operations have no automatic generation tariff")
	}
	r := core.MediaRequest{Model: m.ID, Prompt: strings.Repeat("a", 100), Options: core.MediaOptions{Resolution: rate.Resolution, Duration: rate.Duration}}
	if r.Options.Resolution == "" && len(m.Resolutions) > 0 {
		r.Options.Resolution = m.Resolutions[0]
	}
	if r.Options.Duration == 0 {
		r.Options.Duration = m.DefaultDuration
	}
	if m.NoPrompt {
		r.Prompt = ""
	}
	if m.RequiresImageInput || m.ID == "MiniMax-Hailuo-02" && r.Options.Resolution == "512p" {
		r.Options.ReferenceMode = "first_frame"
		if m.MediaType == "image" || m.ID == "S2V-01" {
			r.Options.ReferenceMode = "references"
		}
		r.Options.ImageURLs = []string{"https://example.invalid/catalogue-reference.png"}
	}
	if rate.VideoInput != nil && *rate.VideoInput {
		r.Options.ReferenceMode = "references"
		r.Options.VideoURL = "https://example.invalid/catalogue-reference.mp4"
		r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.VideoURL: {Type: "video", Duration: 2, AudioStreams: new(int)}}
	}
	if rate.AudioInput != nil && *rate.AudioInput {
		url := "https://example.invalid/catalogue-reference.mp3"
		r.Options.ReferenceMode = "references"
		r.Options.AudioURLs = []string{url}
		if r.Options.ReferenceMediaEvidence == nil {
			r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{}
		}
		r.Options.ReferenceMediaEvidence[url] = core.InputMediaEvidence{Type: "audio", Duration: 2}
	}
	if rate.Audio != nil {
		r.Options.GenerateAudio = rate.Audio
	}
	if m.ID == RegenerationVideo {
		one := 1
		url := "https://example.invalid/catalogue-base.mp4"
		r.Options.ReferenceMode = "references"
		r.Options.VideoURLs = []string{url}
		r.Options.VideoURL = ""
		r.Options.ReferenceRoles = map[string]string{"video_1": "base_video"}
		// Server-only structural sample for a pure tariff check, never a user reference.
		if r.Options.ReferenceMediaEvidence == nil {
			r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{}
		}
		r.Options.ReferenceMediaEvidence[url] = core.InputMediaEvidence{Type: "video", ReferenceID: "00000000-0000-4000-8000-000000000001", SHA256: strings.Repeat("a", 64), ContentType: "video/mp4", SizeBytes: 1, Width: 768, Height: 1024, FPS: 24, FrameCount: 124, Duration: 124.0 / 24, AudioStreams: &one, VideoStreams: &one}
	}
	if err := a.ValidateRequest(context.Background(), r); err != nil {
		return core.MediaRequest{}, err
	}
	return r, nil
}

// CatalogRateQuote supplies only a reviewed metric tariff for source-task
// regeneration. Catalogue refresh must not query an owner's task to validate a
// documented price. Ordinary generation Quote still requires its protected source.
func (a *Adapter) CatalogRateQuote(model core.MediaModel, rate catalogsync.Rate) (core.TaskCost, bool, error) {
	if model.ID != Regeneration {
		return core.TaskCost{}, false, nil
	}
	if rate.Duration != 0 || rate.Resolution != "" || rate.Quality != "" || rate.Audio != nil || rate.VideoInput != nil || rate.AudioInput != nil || rate.ImageInput != nil || rate.VoiceInput != nil || len(rate.Parameters)+len(rate.ResourceInputs)+len(rate.ReferenceRoles) > 0 || rate.VideoInputIncludesResources {
		return core.TaskCost{}, true, invalid("unsupported regeneration tariff selector")
	}
	return core.TaskCost{QuantityUnknown: true, Status: "quantity_unknown", Pricing: RegenerationTariff()}, true, nil
}

// Catalogue Fetch normalizes embedded HTML tables into pipe rows, even inside
// a Markdown source. Support that actual ingestion shape as well as raw fixtures.
func pricingTables(text string) ([][][]string, error) {
	tables := [][][]string{}
	raw := priceTable.FindAllString(text, -1)
	if len(raw) > 0 {
		for _, table := range raw {
			rows, e := tableRows(table)
			if e != nil {
				return nil, e
			}
			tables = append(tables, rows)
		}
		return tables, nil
	}
	var current [][]string
	flush := func() {
		if len(current) > 0 {
			tables = append(tables, current)
			current = nil
		}
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			flush()
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.Join(strings.Fields(strings.ReplaceAll(cells[i], "\\", "")), " ")
		}
		current = append(current, cells)
	}
	flush()
	return tables, nil
}
