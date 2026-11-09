package catalogsync

import (
	"bytes"
	"errors"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"golang.org/x/net/html"
)

// The official release index now uses dated cards instead of tables. Read model
// headings only within each card; replacement links and prose are not releases.
func alibabaReleaseCards(body []byte) ([]Entry, bool, error) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	var entries []Entry
	handled := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		card := false
		for _, attr := range n.Attr {
			if attr.Key == "class" {
				for _, class := range strings.Fields(attr.Val) {
					card = card || class == "rkb-update"
				}
			}
		}
		if card {
			handled = true
			date := ""
			var current *Entry
			var read func(*html.Node)
			read = func(node *html.Node) {
				switch node.Data {
				case "h3":
					date = nodeText(node)
				case "h4":
					current = nil
					id := nodeText(node)
					if alibabaMediaID.FindString(id) == id && id != "" {
						if _, e := time.Parse("2006-01-02", date); e != nil {
							err = errors.New("release card has an invalid effective date")
							return
						}
						entries = append(entries, Entry{ID: id, Metadata: &core.ModelMetadata{ReleaseDate: date}})
						current = &entries[len(entries)-1]
					}
				case "p":
					if current != nil {
						current.Metadata.Description = strings.TrimSpace(current.Metadata.Description + " " + nodeText(node))
					}
				}
				for c := node.FirstChild; c != nil; c = c.NextSibling {
					read(c)
				}
			}
			read(n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if handled && len(entries) == 0 {
		err = errors.New("dated media release cards not found")
	}
	return earliestReleases(entries), handled, err
}

func earliestReleases(entries []Entry) []Entry {
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
	return unique
}
