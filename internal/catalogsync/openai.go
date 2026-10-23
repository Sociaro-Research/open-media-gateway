package catalogsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"golang.org/x/net/html"
)

func attribute(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}
func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// Server-rendered Astro values are JSON, never executable page scripts.
func astroValue(value any) any {
	if pair, ok := value.([]any); ok && len(pair) == 2 {
		if kind, ok := pair[0].(float64); ok {
			if kind == 0 {
				return pair[1]
			}
			if kind == 1 {
				values, ok := pair[1].([]any)
				if !ok {
					return nil
				}
				out := []any{}
				for _, v := range values {
					out = append(out, astroValue(v))
				}
				return out
			}
		}
	}
	return value
}
func openAIHeading(value any) string {
	if label, ok := value.(string); ok {
		return label
	}
	// The official Image table decorates headings with inert tooltip objects.
	if obj, ok := value.(map[string]any); ok {
		if tooltip, ok := astroValue(obj["__pricingTooltipHeading"]).(map[string]any); ok {
			label, _ := astroValue(tooltip["label"]).(string)
			return label
		}
	}
	return ""
}

// Rows omit the grouped Model cell. Resolve price columns by their labels,
// so a presentation-only reorder cannot turn cached input into output pricing.
func openAIPriceColumns(value any) (input, output int, valid bool) {
	headings, ok := astroValue(value).([]any)
	if !ok || len(headings) != 5 || openAIHeading(headings[0]) != "Model" || openAIHeading(headings[1]) != "Modality" {
		return 0, 0, false
	}
	seen := map[string]bool{}
	for index, heading := range headings[2:] {
		label := openAIHeading(heading)
		if label == "Output / cost" {
			label = "Output"
		}
		if seen[label] {
			return 0, 0, false
		}
		seen[label] = true
		switch label {
		case "Input":
			input = index + 1
		case "Output":
			output = index + 1
		case "Cached input":
		default:
			return 0, 0, false
		}
	}
	return input, output, seen["Input"] && seen["Output"] && seen["Cached input"]
}

func parseOpenAIPrices(body []byte) ([]Entry, string, error) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	entries := []Entry{}
	invalidUnits := false
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, batch bool) {
		if v := attribute(n, "data-value"); v != "" && v != "standard" {
			batch = true
		}
		if !batch && n.Data == "astro-island" && attribute(n, "component-export") == "GroupedPricingTable" {
			var props map[string]any
			if json.Unmarshal([]byte(attribute(n, "props")), &props) == nil {
				groups, ok := astroValue(props["groups"]).([]any)
				if ok {
					for _, v := range groups {
						group, ok := v.(map[string]any)
						if !ok {
							continue
						}
						id, ok := astroValue(group["model"]).(string)
						if !ok || (!strings.Contains(id, "image") && !strings.Contains(id, "audio") && !strings.Contains(id, "tts")) {
							continue
						}
						rows, ok := astroValue(group["rows"]).([]any)
						if !ok {
							continue
						}
						unitConfirmed := false
						for ancestor := n.Parent; ancestor != nil; ancestor = ancestor.Parent {
							if strings.Contains(" "+attribute(ancestor, "class")+" ", " pricing-subsection ") {
								unitConfirmed = strings.Contains(nodeText(ancestor), "Prices per 1M tokens.")
								break
							}
						}
						if !unitConfirmed {
							for sibling := n.PrevSibling; sibling != nil; sibling = sibling.PrevSibling {
								if sibling.Type != html.ElementNode {
									continue
								}
								if attribute(sibling, "class") == "pricing-section-meta" {
									unitConfirmed = nodeText(sibling) == "Prices per 1M tokens unless noted."
								}
								break
							}
						}
						if !unitConfirmed {
							invalidUnits = true
							continue
						}
						rate := Rate{Region: "standard", Unit: "multi_metric", Per: 1}
						inputColumn, outputColumn, columnsOK := openAIPriceColumns(props["headings"])
						unsupported := !columnsOK
						for _, row := range rows {
							cells, ok := row.([]any)
							if !columnsOK || !ok || len(cells) != 4 {
								unsupported = true
								continue
							}
							modality, ok := cells[0].(string)
							if !ok {
								unsupported = true
								continue
							}
							prefix := strings.ToLower(modality)
							if prefix != "image" && prefix != "text" && prefix != "audio" {
								unsupported = true
								continue
							}
							// Legacy Speech prices explicitly override the section's token
							// unit. Match the entire cell, never its numeric prefix.
							if value, ok := cells[inputColumn].(string); ok && (id == "tts-1" || id == "tts-1-hd") && prefix == "text" && len(rows) == 1 {
								match := regexp.MustCompile(`^\$([0-9]+(?:\.[0-9]+)?) / 1M characters$`).FindStringSubmatch(value)
								if len(match) == 2 && cells[outputColumn] == "-" {
									price, e := money.Parse(match[1])
									if e == nil {
										rate.Unit, rate.Per, rate.Price = "character", 1_000_000, price
										continue
									}
								}
							}
							for _, c := range []struct {
								index  int
								suffix string
							}{{inputColumn, "_input_tokens"}, {outputColumn, "_output_tokens"}} {
								value, ok := cells[c.index].(float64)
								if !ok {
									if cells[c.index] != "-" {
										unsupported = true
									}
									continue
								}
								p, e := money.Parse(strconv.FormatFloat(value, 'f', -1, 64))
								if e != nil {
									unsupported = true
									continue
								}
								rate.Components = append(rate.Components, core.PricingComponent{Metric: prefix + c.suffix, Price: p, Per: 1_000_000})
							}
						}
						if !unsupported && (len(rate.Components) > 0 || rate.Unit == "character") {
							entries = append(entries, Entry{ID: id, Rates: []Rate{rate}})
						} else {
							entries = append(entries, Entry{ID: id, Metadata: &core.ModelMetadata{Facts: []core.ModelFact{{Label: "Published tariff", Value: string(encoded(rows))}}}})
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, batch)
		}
	}
	walk(root, false)
	if invalidUnits {
		return nil, "", errors.New("Multimodal pricing units changed or were not explicitly confirmed")
	}
	if len(entries) == 0 {
		return nil, "", errors.New("Standard multimodal pricing records were not found")
	}
	return entries, string(encoded(entries)), nil
}
func parseOpenAIModel(source Source, body []byte) ([]Entry, string, error) {
	u, err := url.Parse(source.URL)
	if err != nil {
		return nil, "", err
	}
	id := path.Base(u.Path)
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	description := ""
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inMain bool) {
		inMain = inMain || n.Data == "main"
		if inMain && n.Data == "p" && description == "" {
			value := nodeText(n)
			if len(value) > 30 {
				description = value
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inMain)
		}
	}
	walk(root, false)
	if description == "" || id == "" {
		return nil, "", errors.New("Model description was not found")
	}
	entry := Entry{ID: id, Metadata: &core.ModelMetadata{Description: description}}
	text, err := document(body)
	if err != nil {
		return nil, "", err
	}
	return []Entry{entry}, text, nil
}
