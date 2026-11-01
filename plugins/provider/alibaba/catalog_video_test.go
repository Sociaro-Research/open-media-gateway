package alibaba

import (
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"os"
	"strings"
	"testing"
)

func TestVideoAPIParameterFixtures(t *testing.T) {
	for _, tc := range []struct {
		index       int
		file        string
		ids         int
		maxDuration int
	}{{0, "wan3", 2, 30}, {1, "wan27", 2, 15}, {2, "happyhorse", 1, 15}} {
		t.Run(tc.file, func(t *testing.T) {
			b, e := os.ReadFile("testdata/video-api/" + tc.file + "-parameters.txt")
			if e != nil {
				t.Fatal(e)
			}
			entries, e := interpretVideoAPI(videoAPISources[tc.index], string(b))
			if e != nil || len(entries) != tc.ids {
				t.Fatal(entries, e)
			}
			for _, entry := range entries {
				if entry.Contract != nil || len(entry.Rates) > 0 || len(entry.OperationalFields) == 0 {
					t.Fatal("source evidence became executable or priced", entry)
				}
				var duration map[string]int
				if json.Unmarshal(entry.OperationalFields["output.duration"], &duration) != nil || duration["max"] != tc.maxDuration {
					t.Fatal("duration parsing", entry)
				}
			}
			changed := strings.ReplaceAll(string(b), "duration integer", "duration_variant integer")
			parsed, e := interpretVideoAPI(videoAPISources[tc.index], changed)
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range parsed {
				if _, ok := entry.OperationalFields["output.duration"]; ok {
					t.Fatal("missing parameter replaced by guessed duration")
				}
			}
			if _, e = interpretVideoAPI(videoAPISources[tc.index], "You need to enable JavaScript to run this app."); e == nil {
				t.Fatal("shell accepted as operational API")
			}
			if result, e := interpretVideoAPI(catalogsync.Source{URL: "https://unrelated.example.test"}, string(b)); e != nil || result != nil {
				t.Fatal("interpreted unregistered authority", result, e)
			}
		})
	}
}
func TestVideoAPINeverInventsUndocumentedAlias(t *testing.T) {
	b, e := os.ReadFile("testdata/video-api/wan27-parameters.txt")
	if e != nil {
		t.Fatal(e)
	}
	body := strings.ReplaceAll(string(b), "wan2.7-r2v, ", "")
	entries, e := interpretVideoAPI(videoAPISources[1], body)
	if e != nil || len(entries) != 1 || entries[0].ID != "wan2.7-r2v-2026-06-12" {
		t.Fatal("snapshot name inferred an undocumented alias", entries, e)
	}
}

func TestWan3CombinedDurationIsExactAndNeverGuessed(t *testing.T) {
	body, err := os.ReadFile("testdata/video-api/wan3-parameters.txt")
	if err != nil {
		t.Fatal(err)
	}
	field := "workflow.references.video.input_output_duration"
	for _, tc := range []struct {
		old, replacement string
		maximum          float64
	}{
		{"must not exceed 30 seconds", "must not exceed 30 seconds", 30},
		{"must not exceed 30 seconds", "must not exceed 29 seconds", 29},
		{"must not exceed 30 seconds", "provider-specific duration rule", 0},
	} {
		entries, err := interpretVideoAPI(videoAPISources[0], strings.ReplaceAll(string(body), tc.old, tc.replacement))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			raw, exists := entry.OperationalFields[field]
			if !exists {
				t.Fatal("missing relation lost explicit unknown marker")
			}
			var limit catalogsync.VideoDurationSumLimit
			if err := json.Unmarshal(raw, &limit); err != nil || limit.MaxSeconds != tc.maximum {
				t.Fatal("combined duration guessed or misread", string(raw), err)
			}
		}
	}
}

func TestVideoAPIRejectsDifferentModelTokens(t *testing.T) {
	for _, tc := range []struct {
		source int
		file   string
		ids    []string
	}{
		{0, "wan3", []string{"wan3.0-video-prime", "wan3.0-video"}},
		{1, "wan27", []string{"wan2.7-r2v-2026-06-12", "wan2.7-r2v"}},
		{2, "happyhorse", []string{"happyhorse-1.1-r2v"}},
	} {
		body, err := os.ReadFile("testdata/video-api/" + tc.file + "-parameters.txt")
		if err != nil {
			t.Fatal(err)
		}
		for _, affix := range []string{"prefix", "suffix"} {
			t.Run(tc.file+"/"+affix, func(t *testing.T) {
				text := string(body)
				// Replace longer exact IDs first, in one pass, without rewriting replacements.
				pairs := []string{}
				for _, id := range tc.ids {
					replacement := "future-" + id
					if affix == "suffix" {
						replacement = id + "-future"
					}
					pairs = append(pairs, id, replacement)
				}
				text = strings.NewReplacer(pairs...).Replace(text)
				if entries, err := interpretVideoAPI(videoAPISources[tc.source], text); err == nil || len(entries) != 0 {
					t.Fatal("different models supplied original contract", entries, err)
				}
			})
		}
	}
}
