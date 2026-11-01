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
				v := video
				entry.Rates = append(entry.Rates, catalogsync.Rate{Region: "Global", Resolution: res, VideoInput: &v, Unit: "second", Per: 1, Price: prices[0], Source: source.URL, Components: []core.PricingComponent{{Metric: "output_video_duration", Price: prices[0], Per: 1}, {Metric: "input_video_duration", Price: prices[1], Per: 1}, {Metric: "billable_input_images", Price: prices[2], Per: 1}}})
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
	out := []catalogsync.Entry{}
	for _, id := range []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max", "image-01", "MiniMax-Hailuo-2.3", "MiniMax-Hailuo-2.3-Fast", "MiniMax-Hailuo-02"} {
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
		r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.VideoURL: {Type: "video", Duration: 2}}
	}
	if rate.Audio != nil {
		r.Options.GenerateAudio = rate.Audio
	}
	if err := a.ValidateRequest(context.Background(), r); err != nil {
		return core.MediaRequest{}, err
	}
	return r, nil
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
