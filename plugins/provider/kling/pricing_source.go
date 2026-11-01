package kling

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

const (
	imagePriceSource     = docsBase + "pricing/base/image"
	ecommercePriceSource = docsBase + "pricing/solutions/ecommerce"
	effectsPriceSource   = docsBase + "pricing/solutions/effects"
)

var klingPriceCell = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?) Units? \(\$([0-9]+(?:\.[0-9]+)?)\) /\s*(s|5s|call|image)$`)

// A reviewed row ties exact native operation IDs to an explicit USD Balance
// price and typed selectors. Package units remain separately denominated facts.
type klingPublishedTariff struct {
	Row               string           `json:"row"`
	SourceColumn      string           `json:"source_column,omitempty"`
	PackageUnits      string           `json:"package_units,omitempty"`
	Rate              catalogsync.Rate `json:"rate"`
	UnavailableReason string           `json:"unavailable_reason,omitempty"`
}
type klingPriceVariant struct {
	ids    []string
	rate   catalogsync.Rate
	reason string
}
type klingPriceRow struct {
	key, billing, suffix string
	columns              map[int]string
	variants             []klingPriceVariant
}
type klingPriceTable struct {
	header string
	rows   []klingPriceRow
}

func priceBool(value bool) *bool { return &value }
func priceNoVideoElements() []catalogsync.ResourceCountSelector {
	return []catalogsync.ResourceCountSelector{{Kind: "element", Subtype: "video_character", Min: 0, Max: 0}}
}
func priceVideoVariants(ids []string, audio *bool, video bool) []klingPriceVariant {
	return []klingPriceVariant{{ids: ids, rate: catalogsync.Rate{Audio: audio, VideoInput: priceBool(video)}}}
}
func videoPriceTables() []klingPriceTable {
	out := "Per second (Total duration = Output duration)"
	columns := map[int]string{3: "720p", 4: "1080p", 5: "4k"}
	low := map[int]string{3: "720p", 4: "1080p"}
	high := map[int]string{4: "1080p"}
	call := map[int]string{3: ""}
	row := func(model, feature, billing, suffix string, cols map[int]string, v ...klingPriceVariant) klingPriceRow {
		return klingPriceRow{key: model + "|" + feature, billing: billing, suffix: suffix, columns: cols, variants: v}
	}
	rows := []klingPriceRow{
		row("Kling 4.0", "No Input Video (Input videos include reference videos (feature video and base video), and video character element.)", out, "s", low,
			klingPriceVariant{ids: []string{TextToVideo4, ImageToVideo4}, rate: catalogsync.Rate{VideoInput: priceBool(false)}}, klingPriceVariant{ids: []string{OmniVideo4}, rate: catalogsync.Rate{VideoInput: priceBool(false), VideoInputIncludesResources: true}}),
		row("Kling 4.0", "With Input Video (Input Duration ≤ Output Duration) (Input videos include reference videos (feature video and base video), and video character element.)", "Per second (Total duration = Output duration × 2)", "s", low, klingPriceVariant{ids: []string{OmniVideo4}, rate: catalogsync.Rate{VideoInput: priceBool(true), VideoInputIncludesResources: true, DurationBillingRule: "output_plus_max_input_output"}}),
		row("Kling 4.0", "With Input Video (Input Duration > Output Duration) (Input videos include reference videos (feature video and base video), and video character element.)", "Per second (Total duration = Input duration + Output duration)", "s", low, klingPriceVariant{ids: []string{OmniVideo4}, rate: catalogsync.Rate{VideoInput: priceBool(true), VideoInputIncludesResources: true, DurationBillingRule: "output_plus_max_input_output"}}),
		row("Kling 3.0 Turbo", "With Native Audio", out, "s", low, priceVideoVariants([]string{TextToVideo3Turbo, ImageToVideo3Turbo}, nil, false)...),
		row("Kling 3.0", "No Native Audio", out, "s", columns, priceVideoVariants([]string{TextToVideo, ImageToVideo}, priceBool(false), false)...),
		row("Kling 3.0", "With Native Audio", out, "s", columns, priceVideoVariants([]string{TextToVideo, ImageToVideo}, priceBool(true), false)...),
		row("Kling 3.0", "Motion Control", out, "s", low, priceVideoVariants([]string{MotionControl3}, nil, true)...),
	}
	for _, condition := range []struct {
		feature, audio string
		video          bool
	}{{"No Reference Video Input x No Native Audio (Reference videos include feature video and base video.)", "off", false}, {"No Reference Video Input x With Native Audio (Reference videos include feature video and base video.)", "native", false}, {"With Reference Video Input x No Native Audio (Reference videos include feature video and base video.)", "off", true}} {
		variants := []klingPriceVariant{{ids: []string{OmniVideo3}, rate: catalogsync.Rate{VideoInput: priceBool(condition.video), Parameters: map[string]string{"audio": condition.audio}, ResourceInputs: priceNoVideoElements()}}}
		if condition.video {
			variants = append(variants, klingPriceVariant{ids: []string{OmniVideo3}, rate: catalogsync.Rate{VideoInput: priceBool(true), Parameters: map[string]string{"audio": "original"}, ResourceInputs: priceNoVideoElements()}})
		}
		rows = append(rows, row("Kling 3.0 Omni", condition.feature, out, "s", columns, variants...))
	}
	rows = append(rows,
		row("Kling O1", "No Reference Video Input (Reference videos include feature video and base video.)", out, "s", low, priceVideoVariants([]string{OmniVideoO1}, nil, false)...),
		row("Kling O1", "With Reference Video Input (Reference videos include feature video and base video.)", out, "s", low, priceVideoVariants([]string{OmniVideoO1}, nil, true)...),
		row("Kling 2.6", "No Native Audio", out, "s", low, priceVideoVariants([]string{TextToVideo26, ImageToVideo26}, priceBool(false), false)...),
		row("Kling 2.6", "With Native Audio x No Voice Reference", out, "s", high, klingPriceVariant{ids: []string{TextToVideo26}, rate: catalogsync.Rate{Audio: priceBool(true), VideoInput: priceBool(false)}}, klingPriceVariant{ids: []string{ImageToVideo26}, rate: catalogsync.Rate{Audio: priceBool(true), VideoInput: priceBool(false), ResourceInputs: []catalogsync.ResourceCountSelector{{Kind: "voice", Min: 0, Max: 0}}}}),
		row("Kling 2.6", "With Native Audio x With Voice Reference", out, "s", high, klingPriceVariant{ids: []string{ImageToVideo26}, rate: catalogsync.Rate{Audio: priceBool(true), VideoInput: priceBool(false), ResourceInputs: []catalogsync.ResourceCountSelector{{Kind: "voice", Min: 1, Max: 2}}}}),
		row("Kling 2.6", "Motion Control", out, "s", low, priceVideoVariants([]string{MotionControl26}, nil, true)...),
		row("Kling 2.5 Turbo", "No Native Audio", out, "s", low, priceVideoVariants([]string{TextToVideo25Turbo, ImageToVideo25Turbo}, nil, false)...),
		row("Multi-element Video Editing", "Multi-element Video Editing", "Per 5 seconds (Total duration = Output duration)", "5s", low, klingPriceVariant{ids: []string{MultiElementsEditing}, reason: "Published resolution tariffs do not map the native std/pro modes; mask/session creation tariffs are not verified."}),
		row("Avatar", "Avatar", out, "s", low, klingPriceVariant{ids: []string{AvatarImageToVideo}, reason: "Published 720p/1080p tariffs do not map the native std/pro modes."}),
		row("Avatar", "TTS", "Per request", "call", call, klingPriceVariant{ids: []string{TextToSpeech}}),
		row("Lip Sync", "Lip Sync", "Per 5 seconds", "5s", call, klingPriceVariant{ids: []string{LipSync}, reason: "Published per-5-second tariff does not specify partial-block rounding."}),
		row("Lip Sync", "Face Recognition", "Per request", "call", call, klingPriceVariant{ids: []string{FaceDetection}, rate: catalogsync.Rate{VideoInput: priceBool(true)}}),
		row("Audio Generation", "Text to Audio", "Per request", "call", call, klingPriceVariant{ids: []string{TextToAudio}}),
		row("Audio Generation", "Video to Audio", "Per request", "call", call, klingPriceVariant{ids: []string{VideoToAudio}, rate: catalogsync.Rate{VideoInput: priceBool(true)}}),
		row("Audio Generation", "Custom Voice", "Per request", "call", call, klingPriceVariant{ids: []string{ResourceCreateVoice}}),
		row("Image Recognition", "Image Recognition", "Per request", "call", call, klingPriceVariant{ids: []string{ImageRecognition}}),
	)
	return []klingPriceTable{{header: "Model|Features|Billing Type|Price (720P)|Price (1080P)|Price (4K)", rows: rows}}
}
func imagePriceTables() []klingPriceTable {
	row := func(model, function, resolution string, ids []string, input *bool) klingPriceRow {
		return klingPriceRow{key: model + "|" + function + "|" + resolution, suffix: "image", columns: map[int]string{3: ""}, variants: []klingPriceVariant{{ids: ids, rate: catalogsync.Rate{ImageInput: input}}}}
	}
	rows := []klingPriceRow{
		row("Kling Image 3.0", "Text-to-Image, Image-to-Image", "1K、2K", []string{ImageGeneration3}, nil),
		row("Kling Image 3.0 Omni", "Text-to-Image, Image-to-Image", "1K、2K", []string{ImageOmni3}, nil),
		row("Kling Image 3.0 Omni", "Text-to-Image, Image-to-Image", "4K", []string{ImageOmni3}, nil),
		row("Kling Image O1", "Text-to-Image, Image-to-Image", "1K、2K", []string{ImageO1}, nil),
		row("Kling Image 2.1", "Text-to-Image", "1K、2K", []string{ImageGeneration21}, priceBool(false)),
		row("Kling Image 2.1", "Image-to-Image", "1K、2K", []string{ImageGeneration21}, priceBool(true)),
		row("Kling Image 2.1", "Multi-image to Image", "1K、2K", []string{MultiImage21}, nil),
		row("General", "AI Multi-Shot", "1K", []string{ImageMultiShot}, nil),
		row("General", "Outpainting", "1K", []string{ImageOutpainting}, nil),
	}
	rows[7].suffix = "call"
	return []klingPriceTable{{header: "Model|Function|Resolution|Price", rows: rows}}
}
func ecommercePriceTables() []klingPriceTable {
	out := "Per second (Total duration = Output duration)"
	row := func(name, feature string, variant klingPriceVariant) klingPriceRow {
		return klingPriceRow{key: name + "|" + out + "|" + feature, suffix: "s", columns: map[int]string{3: "720p", 4: "1080p"}, variants: []klingPriceVariant{variant}}
	}
	apparel := func(voice bool) []klingPriceVariant {
		return []klingPriceVariant{{ids: []string{ApparelReplicator}, rate: catalogsync.Rate{VoiceInput: priceBool(voice), VideoInput: priceBool(true), AudioInput: priceBool(false)}}, {ids: []string{ApparelReplicator}, rate: catalogsync.Rate{VoiceInput: priceBool(voice), VideoInput: priceBool(true), AudioInput: priceBool(true)}}}
	}
	rowVariants := func(name, feature string, variants []klingPriceVariant) klingPriceRow {
		r := row(name, feature, variants[0])
		r.variants = variants
		return r
	}
	return []klingPriceTable{
		{header: "Solution|Billing Type|Features|Price (720P)|Price (1080P)|Price (4K)", rows: []klingPriceRow{
			rowVariants("AI Apparel Replicator", "Narration (Voice-over)", apparel(true)),
			rowVariants("AI Apparel Replicator", "Talking Head", apparel(false)),
			row("Goods Studio", "-", klingPriceVariant{ids: []string{GoodsStudio}}),
			row("Video Commerce", "Creator Voiceover", klingPriceVariant{ids: []string{VideoCommerce}, rate: catalogsync.Rate{ReferenceRoles: []catalogsync.RoleCountSelector{{Kind: "image", Role: "ref_image", Min: 0, Max: 0}}}}),
			row("Video Commerce", "Product Voiceover", klingPriceVariant{ids: []string{VideoCommerce}, rate: catalogsync.Rate{ReferenceRoles: []catalogsync.RoleCountSelector{{Kind: "image", Role: "ref_image", Min: 1, Max: 5}}}}),
		}},
		{header: "Solution|Billing Type|Features|Resolution|Price", rows: []klingPriceRow{{key: "Virtual Try-On|Per image|-", billing: "1K", suffix: "image", columns: map[int]string{4: ""}, variants: []klingPriceVariant{{ids: []string{VirtualTryOn}}}}}},
	}
}

func klingSourceTables(source string) ([]klingPriceTable, bool) {
	switch source {
	case priceSource:
		return videoPriceTables(), true
	case imagePriceSource:
		return imagePriceTables(), true
	case ecommercePriceSource:
		return ecommercePriceTables(), true
	case effectsPriceSource:
		return nil, true
	}
	return nil, false
}
func normalizePriceCells(line string) []string {
	cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	for i := range cells {
		cells[i] = strings.Join(strings.Fields(cells[i]), " ")
	}
	return cells
}
func isPriceSeparator(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, "-:") != "" || !strings.Contains(c, "-") {
			return false
		}
	}
	return true
}
func klingPriceRate(source string, text, suffix, resolution string, base catalogsync.Rate) (catalogsync.Rate, string, error) {
	match := klingPriceCell.FindStringSubmatch(text)
	if len(match) != 4 || match[3] != suffix {
		return base, "", errors.New("Kling explicit USD Balance price or billing unit changed")
	}
	units, err := strconv.ParseFloat(match[1], 64)
	if err != nil || units <= 0 {
		return base, "", errors.New("Kling package-unit value is invalid")
	}
	price, err := money.Parse(match[2])
	if err != nil || price <= 0 {
		return base, "", errors.New("Kling explicit USD Balance price is invalid")
	}
	base.Region, base.Resolution, base.Source, base.Price, base.Per = "Singapore", resolution, source, price, 1
	switch suffix {
	case "s":
		base.Unit = "second"
	case "5s":
		base.Unit, base.Per = "second", 5
	case "call":
		base.Unit = "request"
	case "image":
		base.Unit = "image"
	}
	return base, match[1], nil
}
func klingRateKey(rate catalogsync.Rate) string {
	// Price/provenance do not distinguish selectors: conflicting duplicate rows
	// must fail instead of becoming competing tariffs chosen by source order.
	condition := rate.ConditionKey()
	rate.Price, rate.Source = 0, ""
	rate.AudioInput, rate.VoiceInput, rate.ImageInput = nil, nil, nil
	rate.Parameters, rate.ResourceInputs, rate.ReferenceRoles = nil, nil, nil
	rate.VideoInputIncludesResources = false
	raw, _ := json.Marshal(rate)
	return string(raw) + "/" + condition
}

// Dollar amounts are explicitly published Balance prices. Neither package units
// nor another currency are converted, and unknown native mappings remain facts.
func interpretPrices(source catalogsync.Source, text string) ([]catalogsync.Entry, error) {
	canonical := strings.TrimSuffix(source.URL, ".md")
	tables, supported := klingSourceTables(canonical)
	if !supported {
		return nil, nil
	}
	sourceSeen, localeSeen := false, false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if strings.HasPrefix(line, "> Source:") {
			if sourceSeen || line != "> Source: "+canonical {
				return nil, errors.New("Kling pricing source identity changed")
			}
			sourceSeen = true
		}
		if strings.HasPrefix(line, "> Locale:") {
			if localeSeen || line != "> Locale: en" {
				return nil, errors.New("Kling pricing locale changed")
			}
			localeSeen = true
		}
	}
	if !sourceSeen || !localeSeen || strings.Contains(strings.ToLower(text), "preview") {
		return nil, errors.New("Kling pricing source identity/locale is incomplete or unreviewed; retain confirmed rates")
	}
	if canonical == effectsPriceSource {
		headerSeen, rowSeen := false, false
		for _, line := range strings.Split(text, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "|") {
				continue
			}
			cells := normalizePriceCells(line)
			if isPriceSeparator(cells) {
				continue
			}
			key := strings.Join(cells, "|")
			if key == "Function|Resolution|Price" && !headerSeen {
				headerSeen = true
				continue
			}
			if key == "Effects|Pricing varies by template|Effects Pricing List" && headerSeen && !rowSeen {
				rowSeen = true
				continue
			}
			return nil, errors.New("Kling Effects pricing contract changed")
		}
		if !headerSeen || !rowSeen {
			return nil, errors.New("Kling Effects pricing contract is incomplete")
		}
		return []catalogsync.Entry{{ID: KlingEffects, Metadata: &core.ModelMetadata{Facts: []core.ModelFact{{Label: "Published tariff condition", Value: "Effects pricing varies by template; the source supplies no numeric template tariffs."}}}}}, nil
	}
	entries := map[string]*catalogsync.Entry{}
	contracts := map[string][]klingPublishedTariff{}
	selectors := map[string]map[string]catalogsync.Rate{}
	seen := map[string]bool{}
	active := -1
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := normalizePriceCells(line)
		if isPriceSeparator(cells) {
			continue
		}
		header := strings.Join(cells, "|")
		matchedHeader := false
		for i, t := range tables {
			if header == t.header {
				active = i
				matchedHeader = true
				break
			}
		}
		if matchedHeader {
			continue
		}
		if active < 0 {
			return nil, errors.New("Kling pricing header changed")
		}
		t := tables[active]
		if len(cells) != len(strings.Split(t.header, "|")) {
			return nil, errors.New("Kling pricing column count changed")
		}
		keyCols := 2
		if canonical == imagePriceSource || canonical == ecommercePriceSource {
			keyCols = 3
		}
		key := strings.Join(cells[:keyCols], "|")
		var row *klingPriceRow
		for i := range t.rows {
			if t.rows[i].key == key {
				row = &t.rows[i]
				break
			}
		}
		if row == nil {
			return nil, errors.New("Kling pricing row or condition changed; adapter review is required")
		}
		unique := fmt.Sprint(active) + "/" + key
		if seen[unique] {
			return nil, errors.New("Kling pricing row duplicated")
		}
		seen[unique] = true
		if row.billing != "" && cells[keyCols] != row.billing {
			return nil, errors.New("Kling billing formula changed")
		}
		if len(cells) == 6 {
			for i := 3; i < 6; i++ {
				if _, expected := row.columns[i]; !expected && cells[i] != "-" {
					return nil, errors.New("Kling pricing adds an unreviewed resolution")
				}
			}
		}
		indexes := make([]int, 0, len(row.columns))
		for i := range row.columns {
			indexes = append(indexes, i)
		}
		sort.Ints(indexes)
		for _, column := range indexes {
			resolutions := []string{row.columns[column]}
			if canonical == imagePriceSource && row.variants[0].ids[0] != MultiImage21 && row.variants[0].ids[0] != ImageOutpainting && row.variants[0].ids[0] != ImageMultiShot {
				resolutions = strings.Split(strings.ToLower(cells[2]), "、")
			}
			for _, resolution := range resolutions {
				for _, variant := range row.variants {
					rate, units, err := klingPriceRate(source.URL, cells[column], row.suffix, resolution, variant.rate)
					if err != nil {
						return nil, err
					}
					for _, id := range variant.ids {
						if entries[id] == nil {
							entries[id] = &catalogsync.Entry{ID: id}
							selectors[id] = map[string]catalogsync.Rate{}
						}
						contracts[id] = append(contracts[id], klingPublishedTariff{Row: key, SourceColumn: strings.Split(t.header, "|")[column], PackageUnits: units, Rate: rate, UnavailableReason: variant.reason})
						if variant.reason != "" {
							continue
						}
						selector := klingRateKey(rate)
						if previous, ok := selectors[id][selector]; ok {
							if previous.Price != rate.Price {
								return nil, errors.New("Kling duplicate selector prices disagree; a new estimator is required")
							}
							continue
						}
						selectors[id][selector] = rate
						entries[id].Rates = append(entries[id].Rates, rate)
					}
				}
			}
		}
	}
	for i, t := range tables {
		for _, r := range t.rows {
			if !seen[fmt.Sprint(i)+"/"+r.key] {
				return nil, errors.New("Kling reviewed pricing rows are missing; retain confirmed rates")
			}
		}
	}
	if canonical == priceSource {
		entries[ResourceCreateElement] = &catalogsync.Entry{ID: ResourceCreateElement, Metadata: &core.ModelMetadata{Facts: []core.ModelFact{{Label: "Published tariff condition", Value: "No reviewed numeric element-creation tariff is published; absence is not zero or free."}}}}
	}
	result := make([]catalogsync.Entry, 0, len(entries))
	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := entries[id]
		if len(contracts[id]) > 0 {
			raw, _ := json.Marshal(contracts[id])
			e.OperationalFields = map[string]json.RawMessage{"kling_tariff_contract": raw}
			reasons := map[string]bool{}
			for _, c := range contracts[id] {
				if c.UnavailableReason != "" {
					reasons[c.UnavailableReason] = true
				}
			}
			if id == ApparelReplicator || id == VideoCommerce {
				reasons["Published per-second rate is known; output quantity is unknown until generated, and is never represented as a zero/free estimate."] = true
			}
			if id == OmniVideo3 {
				reasons["Video-character element tariff classification is not established by this published feature/base-video table."] = true
			}
			if len(reasons) > 0 {
				if e.Metadata == nil {
					e.Metadata = &core.ModelMetadata{}
				}
				texts := make([]string, 0, len(reasons))
				for reason := range reasons {
					texts = append(texts, reason)
				}
				sort.Strings(texts)
				e.Metadata.Facts = append(e.Metadata.Facts, core.ModelFact{Label: "Published tariff condition", Value: strings.Join(texts, " ")})
			}
		}
		result = append(result, *e)
	}
	return result, nil
}
