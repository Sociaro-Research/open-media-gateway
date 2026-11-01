package catalogsync

import (
	"reflect"
	"strings"
	"testing"
)

func TestBytePlusGroupedModelHeaders(t *testing.T) {
	const id = "dreamina-seedance-2-5-260628"
	want := []string{"Provider: Default / Max RPM", "Provider: Default / Max concurrency", "Provider: Flex"}
	for name, body := range map[string]string{
		"markdown":                 "|Model ID|Default||Flex|\n|---|---|---|---|\n||Max RPM|Max concurrency||\n|" + id + "|Enterprise: 600 Individual: 180|Enterprise: 10 Individual: 3|Not supported|",
		"html colspan":             "<table><tr><th>Model ID</th><th colspan='2'>Default</th><th>Flex</th></tr><tr><th></th><th>Max RPM</th><th>Max concurrency</th><th></th></tr><tr><td>" + id + "</td><td>Enterprise: 600 Individual: 180</td><td>Enterprise: 10 Individual: 3</td><td>Not supported</td></tr></table>",
		"html rowspan and colspan": "<table><tr><th rowspan='2'>Model ID</th><th colspan='2'>Default</th><th rowspan='2'>Flex</th></tr><tr><th>Max RPM</th><th>Max concurrency</th></tr><tr><td>" + id + "</td><td>Enterprise: 600 Individual: 180</td><td>Enterprise: 10 Individual: 3</td><td>Not supported</td></tr></table>",
	} {
		t.Run(name, func(t *testing.T) {
			entries, _, err := Parse(Source{Parser: "byteplus-models"}, []byte(body))
			if err != nil || len(entries) != 1 {
				t.Fatalf("entries=%+v err=%v", entries, err)
			}
			var labels, values []string
			for _, fact := range entries[0].Metadata.Facts {
				labels = append(labels, fact.Label)
				values = append(values, fact.Value)
			}
			if !reflect.DeepEqual(labels, want) {
				t.Fatalf("grouped labels lost: %q", labels)
			}
			if !reflect.DeepEqual(values, []string{"Enterprise: 600 Individual: 180", "Enterprise: 10 Individual: 3", "Not supported"}) {
				t.Fatal("grouped values changed", values)
			}
			if entries[0].Contract != nil || len(entries[0].Rates) != 0 {
				t.Fatal("header evidence became an executable contract or price")
			}
		})
	}
}

func TestBytePlusGroupedHeadersPreserveUnknownFieldsAndReset(t *testing.T) {
	body := "|Model ID|Default||Flex|\n|---|---|---|---|\n||Max RPM|Future limit||\n|seedance-1-0-pro-250528|600|10|TPD 500B|\n\n## Image limits\n|Model ID|Rate limits (Max IPM)|\n|---|---|\n|seedream-4-5-251128|500|"
	entries, _, err := Parse(Source{Parser: "byteplus-models"}, []byte(body))
	if err != nil || len(entries) != 2 {
		t.Fatal(entries, err)
	}
	facts := entries[0].Metadata.Facts
	if len(facts) != 3 || facts[1].Label != "Provider: Default / Future limit" || facts[1].Value != "10" {
		t.Fatal("unknown child was lost or guessed", facts)
	}
	if facts := entries[1].Metadata.Facts; len(facts) != 1 || facts[0].Label != "Provider: default max images/minute (account-specific limits may differ)" {
		t.Fatal("group leaked into next table", facts)
	}
}

func TestBytePlusIncompleteGroupedHeadersDoNotGuessChildren(t *testing.T) {
	for name, child := range map[string]string{"missing": "", "wrong width": "||Max RPM|\n"} {
		t.Run(name, func(t *testing.T) {
			body := "|Model ID|Default||Flex|\n|---|---|---|---|\n" + child + "|seedance-1-0-pro-250528|600|10|TPD 500B|"
			entries, text, err := Parse(Source{Parser: "byteplus-models"}, []byte(body))
			if err != nil || len(entries) != 1 {
				t.Fatal(entries, err)
			}
			seen := map[string]bool{}
			for _, f := range entries[0].Metadata.Facts {
				if f.Label == "Provider: " || seen[f.Label] || strings.Contains(f.Label, "Max concurrency") {
					t.Fatal("incomplete header guessed or lost a column", f)
				}
				seen[f.Label] = true
			}
			if len(seen) != 3 || !strings.Contains(text, "|600|10|TPD 500B|") {
				t.Fatal("unreviewed column values lost")
			}
		})
	}
}
