package catalogsync

import (
	"bytes"
	"errors"
	"regexp"
	"strings"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"golang.org/x/net/html"
)

var alibabaMediaID = regexp.MustCompile(`\b(?:vidu/[a-zA-Z0-9_-]+|happyhorse-[a-zA-Z0-9._-]*|qwen-mt-image[a-zA-Z0-9._-]*|qwen-image[a-zA-Z0-9._-]*|qwen-audio-[0-9.]+-tts[a-zA-Z0-9._-]*|qwen3-tts[a-zA-Z0-9._-]*|qwen-tts[a-zA-Z0-9._-]*|cosyvoice-[a-zA-Z0-9._-]*|wan[0-9x][a-zA-Z0-9._-]*|z-image[a-zA-Z0-9._-]*)\b`)
var alibabaAmount = regexp.MustCompile(`^\$([0-9]+(?:\.[0-9]+)?)/(image|second)$`)

// Preserve regional heading scope through lower-level subheadings, expand HTML
// rowspans, and keep conditional prices distinct. Unknown conditions remain
// structured evidence requiring review; they never become scalar prices.
func parseAlibabaPrices(body []byte) ([]Entry, string, error) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	var entries []Entry
	region, depth := "", 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(n.Data) == 2 && n.Data[0] == 'h' && n.Data[1] >= '2' && n.Data[1] <= '6' {
			level := int(n.Data[1] - '0')
			heading := nodeText(n)
			switch heading {
			case "Singapore":
				region, depth = "Singapore", level
			case "China (Beijing)":
				region, depth = "Beijing", level
			default:
				if level <= depth && heading != "Qwen3-TTS-Flash" && heading != "Qwen3-TTS-Flash-Realtime" {
					region = ""
				}
			}
		}
		if n.Data == "table" && region != "" {
			rows := htmlTableRows(n)
			if len(rows) < 2 {
				return
			}
			header := rows[0]
			modelCol, priceCol, inputCol, resCol, conditionCol := -1, -1, -1, -1, -1
			speech := false
			unknownColumn := false
			for i, h := range header {
				h = strings.ToLower(h)
				switch {
				case strings.Contains(h, "model id"):
					modelCol = i
				case strings.Contains(h, "input") && strings.Contains(h, "10,000 characters"):
					priceCol = i
					speech = true
				case h == "output price" || h == "input and output price" || h == "output unit price" || h == "input and output unit price" || h == "unit price":
					if !speech {
						priceCol = i
					}
				case h == "input unit price" || h == "input price":
					inputCol = i
				case strings.Contains(h, "resolution"):
					resCol = i
				case h == "output video type" || h == "output video mode":
					conditionCol = i
				case strings.Contains(h, "deployment scope") || strings.Contains(h, "quota") || speech && strings.Contains(h, "output price"):
				default:
					unknownColumn = true
				}
			}
			if modelCol < 0 {
				return
			}
			if priceCol < 0 {
				// Recognized media IDs in a new billing format must remain visible
				// candidates. Do not silently discard token-based audio tables.
				for _, row := range rows[1:] {
					if len(row) <= modelCol {
						continue
					}
					id := alibabaMediaID.FindString(row[modelCol])
					if id == "" {
						continue
					}
					entries = append(entries, Entry{ID: id, Metadata: &core.ModelMetadata{Facts: []core.ModelFact{{Label: "Published tariff condition: " + region + " / " + strings.Join(header, " / "), Value: strings.Join(row, " | ")}}}})
				}
				return
			}
			for _, row := range rows[1:] {
				if len(row) <= priceCol || len(row) <= modelCol {
					continue
				}
				id := alibabaMediaID.FindString(row[modelCol])
				if id == "" {
					continue
				}
				selector := region
				if resCol >= 0 && resCol < len(row) {
					selector += " / " + row[resCol]
				}
				if conditionCol >= 0 && conditionCol < len(row) {
					selector += " / " + row[conditionCol]
				}
				entry := Entry{ID: id, Metadata: &core.ModelMetadata{Facts: []core.ModelFact{{Label: "Published tariff: " + selector, Value: strings.Join(row, " | ")}}}}
				value := strings.TrimSpace(row[priceCol])
				if id == "wan2.2-s2v" {
					for _, part := range regexp.MustCompile(`(480|720)P:\s*\$([0-9.]+)/second`).FindAllStringSubmatch(value, -1) {
						price, e := money.Parse(part[2] + "e3")
						if e == nil {
							entry.Rates = append(entry.Rates, Rate{Region: region, Resolution: part[1] + "p", Unit: "second", Per: 1000, Price: price})
						}
					}
					entries = append(entries, entry)
					continue
				}
				if id == "z-image-turbo" {
					// The adapter uses the documented default prompt_extend=true. Retain the
					// disabled option as evidence without substituting its cheaper price.
					m := regexp.MustCompile(`(?s)prompt_extend\s*=\s*true[^$]*\$\s*([0-9.]+)\s*/\s*image`).FindStringSubmatch(value)
					if m != nil {
						value = "$" + m[1] + "/image"
					}
				}
				if strings.HasPrefix(value, "List price") {
					value = strings.TrimSpace(strings.TrimPrefix(value, "List price"))
					if i := strings.Index(value, "(Limited-time"); i >= 0 {
						value = strings.TrimSpace(value[:i])
					}
				}
				value = strings.Join(strings.Fields(value), "")
				if speech {
					value += "/character"
				}
				match := alibabaAmount.FindStringSubmatch(value)
				if speech {
					match = regexp.MustCompile(`^\$([0-9]+(?:\.[0-9]+)?)/(character)$`).FindStringSubmatch(value)
				}
				valid := match != nil && !unknownColumn
				rate := Rate{Region: region, Per: 1}
				if valid {
					amount := match[1]
					rate.Unit = match[2]
					if rate.Unit == "second" {
						amount += "e3"
						rate.Per = 1000
					}
					if rate.Unit == "character" {
						rate.Per = 10000
					}
					rate.Price, err = money.Parse(amount)
					valid = err == nil
				}
				if conditionCol >= 0 && conditionCol < len(row) {
					condition := strings.ToLower(row[conditionCol])
					switch {
					case strings.Contains(condition, "audio=true"):
						v := true
						rate.Audio = &v
					case strings.Contains(condition, "audio=false"):
						v := false
						rate.Audio = &v
					case condition == "audio video" || condition == "silent video": // fixed model behavior, not a selectable tariff
					case strings.Contains(condition, "wan-std"): // reviewed adapter default
					case strings.Contains(condition, "wan-pro"):
						entry.Metadata.Facts[0].Label = "Documented optional tariff: " + selector
						entries = append(entries, entry)
						continue
					default:
						valid = false
					}
				}
				if inputCol >= 0 && inputCol < len(row) {
					in := alibabaAmount.FindStringSubmatch(strings.Join(strings.Fields(row[inputCol]), ""))
					if in == nil || in[2] != "image" || rate.Unit != "image" {
						valid = false
					} else {
						p, e := money.Parse(in[1])
						if e != nil {
							valid = false
						} else {
							rate.InputImages = &ImageInputTariff{Price: p}
						}
					}
				}
				if resCol >= 0 && resCol < len(row) {
					rate.Resolution = strings.ToLower(row[resCol])
				}
				if valid {
					for _, resolution := range alibabaResolutions(id, rate.Resolution) {
						copy := rate
						copy.Resolution = resolution
						if alibabaVideoSelectors(id) {
							for _, hasVideo := range []bool{false, true} {
								v := hasVideo
								variant := copy
								variant.VideoInput = &v
								entry.Rates = append(entry.Rates, variant)
							}
						} else {
							entry.Rates = append(entry.Rates, copy)
						}
					}
				}
				entries = append(entries, entry)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if len(entries) == 0 {
		return nil, "", errors.New("Regional media tariffs were not found")
	}
	return entries, string(encoded(entries)), nil
}

func alibabaVideoSelectors(id string) bool {
	return strings.HasPrefix(id, "wan3.0-") || strings.HasPrefix(id, "wan2.7-i2v") || strings.HasPrefix(id, "wan2.7-r2v") || strings.HasPrefix(id, "wan2.6-r2v") || id == "wan2.7-videoedit" || id == "happyhorse-1.0-video-edit"
}

func parseAlibabaRetirements(body []byte) ([]Entry, string, error) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	text := nodeText(root)
	date := regexp.MustCompile(`(?:Affected time\s+)(\d{4}-\d{2}-\d{2})`).FindStringSubmatch(text)
	if date == nil {
		return nil, "", errors.New("Explicit retirement effective date was not found")
	}
	var entries []Entry
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Data == "table" {
			rows := htmlTableRows(n)
			if len(rows) < 2 || len(rows[0]) == 0 || !strings.Contains(rows[0][0], "Model Name") {
				return
			}
			for _, row := range rows[1:] {
				if len(row) < 4 {
					continue
				}
				id := alibabaMediaID.FindString(row[0])
				if id == "" {
					continue
				}
				entries = append(entries, Entry{ID: id, Metadata: &core.ModelMetadata{ShutdownDate: date[1], Facts: []core.ModelFact{{Label: "Lifecycle status", Value: "Retired by provider"}, {Label: "Provider replacement", Value: row[3] + "; replacement requires a new explicit model selection."}}}})
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if len(entries) == 0 {
		return nil, "", errors.New("Retired media models were not found")
	}
	return entries, string(encoded(entries)), nil
}

func alibabaResolutions(id, tier string) []string {
	if id == "qwen-image-3.0" || id == "qwen-image-3.0-pro" {
		switch tier {
		case "1k":
			return []string{"1024*1024"}
		case "2k":
			return []string{"1536*1024", "1024*1536", "2048*2048"}
		}
	}
	if id == "vidu/vidu-image_reference2image" {
		switch tier {
		case "1k":
			return []string{"1024*1024"}
		case "2k":
			return []string{"2048*2048"}
		case "4k":
			return []string{"4096*4096"}
		}
	}
	return []string{tier}
}

func parseAlibabaImages(body []byte) ([]Entry, string, error) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	var entries []Entry
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Data == "table" {
			rows := htmlTableRows(n)
			if len(rows) < 2 || len(rows[0]) == 0 || rows[0][0] != "Model ID" {
				return
			}
			for _, row := range rows[1:] {
				if len(row) != len(rows[0]) {
					continue
				}
				id := alibabaMediaID.FindString(row[0])
				if id == "" {
					continue
				}
				facts := []core.ModelFact{}
				for i, h := range rows[0][1:] {
					if h == "Use cases" {
						continue
					}
					facts = append(facts, core.ModelFact{Label: "Provider: " + h, Value: row[i+1]})
				}
				entries = append(entries, Entry{ID: id, Metadata: &core.ModelMetadata{Facts: facts}})
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if len(entries) == 0 {
		return nil, "", errors.New("Structured image model capabilities were not found")
	}
	return entries, string(encoded(entries)), nil
}
