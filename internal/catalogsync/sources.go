package catalogsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelcatalog"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"golang.org/x/net/html"
)

func SupportedParser(p string) bool {
	switch p {
	case "document", "manifest-v1", "byteplus-releases", "byteplus-models", "byteplus-pricing", "alibaba-releases", "alibaba-pricing", "alibaba-retirements", "alibaba-image-models", "openai-pricing", "openai-model":
		return true
	}
	return false
}
func Fetch(ctx context.Context, client *http.Client, source Source) Observation {
	ob := Observation{Source: source, Entries: []Entry{}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err == nil {
		req.Header.Set("User-Agent", "Sociaro-Gateway-CatalogSync/1.0")
		req.Header.Set("Accept", "text/html")
		if source.Parser == "manifest-v1" {
			req.Header.Set("Accept", "application/json")
		}
	}
	if err != nil {
		ob.Error = "Invalid source request"
		return ob
	}
	res, err := client.Do(req)
	if err != nil {
		ob.Error = "Source request failed"
		return ob
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		ob.Error = fmt.Sprintf("Source returned HTTP %d", res.StatusCode)
		return ob
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if err != nil || len(body) > 8<<20 {
		ob.Error = "Source exceeded the size limit or could not be read"
		return ob
	}
	entries, text, err := Parse(source, body)
	if err != nil {
		ob.Error = err.Error()
		if evidence, e := document(body); e == nil && len(evidence) <= 2<<20 {
			ob.Text = evidence
			hash := sha256.Sum256([]byte(evidence))
			ob.FetchedHash = hex.EncodeToString(hash[:])
		}
		return ob
	}
	if len(text) > 2<<20 {
		ob.Error = "Extracted document exceeded the size limit"
		return ob
	}
	if entries == nil {
		entries = []Entry{}
	}
	ob.Entries, ob.Text = entries, text
	hash := sha256.Sum256([]byte(text))
	ob.Hash = hex.EncodeToString(hash[:])
	return ob
}
func clean(s string) string {
	s = strings.NewReplacer("\\-", "-", "\\>", ">").Replace(s)
	s = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(strings.NewReplacer("*", "", "`", "", "\u200b", "").Replace(s)), " ")
}
func document(body []byte) (string, error) {
	s := string(body)
	if i := strings.Index(s, "window._ROUTER_DATA"); i >= 0 {
		tail := s[i:]
		if i = strings.Index(tail, "="); i >= 0 {
			var tree any
			if json.NewDecoder(strings.NewReader(tail[i+1:])).Decode(&tree) == nil {
				var md string
				var walk func(any)
				walk = func(v any) {
					switch x := v.(type) {
					case map[string]any:
						if text, ok := x["MDContent"].(string); ok && len(text) > len(md) {
							md = text
						}
						for _, v := range x {
							walk(v)
						}
					case []any:
						for _, v := range x {
							walk(v)
						}
					}
				}
				walk(tree)
				if md != "" {
					return strings.ReplaceAll(md, "\\-", "-"), nil
				}
			}
		}
	}
	if !strings.Contains(s, "<html") && !strings.Contains(s, "<table") {
		return s, nil
	}
	root, e := html.Parse(bytes.NewReader(body))
	if e != nil {
		return "", e
	}
	var article *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if article == nil && (n.Data == "article" || n.Data == "main") {
			article = n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(root)
	if article != nil {
		root = article
	}
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "nav" || n.Data == "footer") {
			return
		}
		if n.Type == html.ElementNode && n.Data == "table" {
			for _, row := range htmlTableRows(n) {
				out.WriteString("\n|" + strings.Join(row, "|") + "|\n")
			}
			return
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "tr":
				out.WriteString("\n|")
			case "h1", "h2", "h3", "h4":
				out.WriteString("\n# ")
			case "p", "li":
				out.WriteString("\n")
			}
		}
		if n.Type == html.TextNode {
			out.WriteString(strings.Join(strings.Fields(n.Data), " "))
			out.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "th", "td":
				out.WriteString("|")
			case "h1", "h2", "h3", "h4", "tr", "p":
				out.WriteByte('\n')
			}
		}
	}
	walk(root)
	text := out.String()
	if len(strings.TrimSpace(text)) < 40 {
		return "", errors.New("Document content was not found")
	}
	return text, nil
}

func htmlTableRows(table *html.Node) [][]string {
	type span struct {
		value string
		left  int
	}
	spans := map[int]span{}
	rows := [][]string{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Data == "tr" {
			row := []string{}
			col := 0
			fill := func() {
				for {
					s, ok := spans[col]
					if !ok {
						break
					}
					row = append(row, s.value)
					s.left--
					if s.left == 0 {
						delete(spans, col)
					} else {
						spans[col] = s
					}
					col++
				}
			}
			for cell := n.FirstChild; cell != nil; cell = cell.NextSibling {
				if cell.Data != "td" && cell.Data != "th" {
					continue
				}
				fill()
				value := nodeText(cell)
				rowspan, _ := strconv.Atoi(attribute(cell, "rowspan"))
				colspan, _ := strconv.Atoi(attribute(cell, "colspan"))
				if colspan < 1 || colspan > 100 {
					colspan = 1
				}
				if rowspan > 1000 {
					rowspan = 1
				}
				for i := 0; i < colspan; i++ {
					row = append(row, value)
					if rowspan > 1 {
						spans[col] = span{value: value, left: rowspan - 1}
					}
					col++
				}
			}
			fill()
			rows = append(rows, row)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(table)
	return rows
}

var mediaID = regexp.MustCompile(`(?:dreamina-seedance|seedance|dola-seedream|seedream|qwen-image|qwen3-tts|qwen-tts|wan[0-9x]|z-image|gpt-image|gpt-audio|tts-)[a-zA-Z0-9._/-]*`)

func Parse(source Source, body []byte) ([]Entry, string, error) {
	if source.Parser == "alibaba-pricing" {
		return parseAlibabaPrices(body)
	}
	if source.Parser == "alibaba-retirements" {
		return parseAlibabaRetirements(body)
	}
	if source.Parser == "alibaba-image-models" {
		return parseAlibabaImages(body)
	}
	if source.Parser == "openai-pricing" {
		return parseOpenAIPrices(body)
	}
	if source.Parser == "openai-model" {
		return parseOpenAIModel(source, body)
	}
	if source.Parser == "manifest-v1" {
		var payload struct {
			Models []Entry `json:"models"`
		}
		d := json.NewDecoder(bytes.NewReader(body))
		d.DisallowUnknownFields()
		if e := d.Decode(&payload); e != nil || payload.Models == nil || len(payload.Models) > 10000 {
			return nil, "", errors.New("Invalid catalogue manifest")
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			return nil, "", errors.New("Invalid trailing catalogue content")
		}
		// A missing numeric field otherwise decodes as zero. Only an explicitly
		// published zero may become a free tariff; component rates need each price.
		var prices struct {
			Models []struct {
				Rates []struct {
					Price       *money.USD `json:"price"`
					InputImages *struct {
						Price *money.USD `json:"price"`
					} `json:"input_images"`
					Components []struct {
						Price *money.USD `json:"price"`
					} `json:"components"`
				} `json:"rates"`
			} `json:"models"`
		}
		if json.Unmarshal(body, &prices) != nil {
			return nil, "", errors.New("Invalid catalogue prices")
		}
		for _, model := range prices.Models {
			for _, rate := range model.Rates {
				if rate.InputImages != nil && rate.InputImages.Price == nil {
					return nil, "", errors.New("Catalogue reference-image tariff is missing its price")
				}
				if len(rate.Components) == 0 && rate.Price == nil {
					return nil, "", errors.New("Catalogue tariff is missing its price")
				}
				for _, component := range rate.Components {
					if component.Price == nil {
						return nil, "", errors.New("Catalogue usage metric is missing its price")
					}
				}
			}
		}
		seen := map[string]bool{}
		for _, m := range payload.Models {
			if m.ID == "" || len(m.ID) > 256 || seen[m.ID] {
				return nil, "", errors.New("Invalid or duplicate manifest model")
			}
			seen[m.ID] = true
		}
		return payload.Models, string(encoded(payload)), nil
	}
	text, e := document(body)
	if e != nil {
		return nil, "", e
	}
	if source.Parser == "byteplus-pricing" {
		if entries, handled := bytePlusMediaTables(text); handled {
			if len(entries) == 0 {
				return nil, "", errors.New("Source format changed or no model records were recognized")
			}
			return entries, text, nil
		}
	}
	if source.Parser == "document" {
		if len(strings.TrimSpace(text)) < 80 {
			return nil, "", errors.New("Source document is empty or incomplete")
		}
		return nil, text, nil
	}
	var entries []Entry
	month := ""
	pricingUnit := ""
	var modelColumns []string
	var modelGroups []string
	groupedModelColumns := false
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if m := regexp.MustCompile(`^#+\s*(20\d{2})[-/]?(\d{2})\s*$`).FindStringSubmatch(trim); m != nil {
			month = m[1] + "-" + m[2]
		}
		if !strings.HasPrefix(trim, "|") {
			if source.Parser == "byteplus-models" && trim != "" {
				modelColumns, modelGroups = nil, nil
				groupedModelColumns = false
			}
			continue
		}
		row := strings.Trim(trim, "|")
		if source.Parser == "byteplus-models" {
			// Empty edge cells belong to grouped headers, not delimiters.
			row = strings.TrimSuffix(strings.TrimPrefix(trim, "|"), "|")
		}
		cells := strings.Split(row, "|")
		for i := range cells {
			cells[i] = clean(cells[i])
		}
		if source.Parser == "byteplus-models" && len(modelGroups) > 0 && bytePlusModelChildHeader(modelGroups, cells) {
			if len(cells) == len(modelGroups) {
				modelColumns = bytePlusGroupedModelColumns(modelGroups, cells)
			}
			modelGroups = nil
			continue
		}
		if source.Parser == "byteplus-models" && len(cells) >= 2 && cells[0] == "Model ID" {
			modelColumns = append([]string(nil), cells...)
			modelGroups = nil
			groupedModelColumns = false
			for i, column := range cells[1:] {
				if column == "" || i > 0 && column == cells[i] {
					groupedModelColumns = true
				}
			}
			if groupedModelColumns {
				modelGroups = append([]string(nil), cells...)
				modelColumns = bytePlusGroupedModelColumns(modelGroups, nil)
			}
			continue
		}
		if source.Parser == "byteplus-pricing" && len(cells) >= 3 && cells[0] == "Model ID" {
			pricingUnit = ""
			if cells[1] == "Online inference (USD / M tokens)" {
				pricingUnit = "token"
			}
			if cells[1] == "Input image price (USD / image)" && cells[2] == "Output image price (USD / image)" {
				pricingUnit = "image"
			}
			continue
		}
		switch source.Parser {
		case "byteplus-releases":
			if len(cells) < 5 || month == "" {
				continue
			}
			id := mediaID.FindString(cells[1])
			if id == "" {
				continue
			}
			if len(cells) > 5 && regexp.MustCompile(`^\d{6}$`).MatchString(cells[3]) {
				id += "-" + cells[3]
			}
			id = modelcatalog.CanonicalID("bytedance", id)
			entries = append(entries, Entry{ID: id, Metadata: &core.ModelMetadata{ReleaseDate: month, Description: cells[len(cells)-1]}})
		case "alibaba-releases":
			if len(cells) < 5 || !ValidDate(cells[1]) || cells[1] == "" {
				continue
			}
			// The same ID can have different regional dates; store its earliest
			// public release, without presenting a regional date as universal.
			for _, id := range alibabaMediaID.FindAllString(cells[3], -1) {
				entries = append(entries, Entry{ID: id, Metadata: &core.ModelMetadata{ReleaseDate: cells[1], Description: cells[len(cells)-1]}})
			}
		case "byteplus-models":
			if len(cells) < 2 || len(modelColumns) != len(cells) {
				continue
			}
			id := modelcatalog.CanonicalID("bytedance", mediaID.FindString(cells[0]))
			if id == "" {
				continue
			}
			modelGroups = nil
			facts := []core.ModelFact{}
			for i, column := range modelColumns[1:] {
				label := "Provider: " + column
				if !groupedModelColumns {
					switch column {
					case "Capabilities":
						label = "Provider: reference workflows"
					case "Output video format":
						label = "Provider: output specifications / limits"
					case "Rate limits (Max IPM)":
						label = "Provider: default max images/minute (account-specific limits may differ)"
					default:
						if strings.HasPrefix(column, "Rate limits") {
							label = "Provider: default rate limits (account-specific limits may differ)"
						}
					}
				}
				facts = append(facts, core.ModelFact{Label: label, Value: cells[i+1]})
			}
			entries = append(entries, Entry{ID: id, Metadata: &core.ModelMetadata{Facts: facts}})
		case "byteplus-pricing":
			if len(cells) < 3 {
				continue
			}
			id := modelcatalog.CanonicalID("bytedance", mediaID.FindString(cells[0]))
			if id == "" {
				continue
			}
			entries = append(entries, bytePlusTariff(id, pricingUnit, cells))
		}
	}
	if len(entries) == 0 {
		return nil, "", errors.New("Source format changed or no model records were recognized")
	}
	// Release tables may list the same ID several times, newest first.
	if source.Parser == "alibaba-releases" || source.Parser == "byteplus-releases" {
		index := map[string]int{}
		var unique []Entry
		for _, entry := range entries {
			if i, ok := index[entry.ID]; ok {
				if entry.Metadata.ReleaseDate < unique[i].Metadata.ReleaseDate {
					unique[i] = entry
				}
			} else {
				index[entry.ID] = len(unique)
				unique = append(unique, entry)
			}
		}
		entries = unique
	}
	return entries, text, nil
}
