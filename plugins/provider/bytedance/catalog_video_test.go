package bytedance

import (
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"os"
	"strings"
	"testing"
)

func TestSeedanceTaskAPIFactualFixture(t *testing.T) {
	b, e := os.ReadFile("testdata/video-api/seedance-parameters.txt")
	if e != nil {
		t.Fatal(e)
	}
	entries, e := interpretVideoAPI(videoAPISource, string(b))
	if e != nil || len(entries) != 1 || entries[0].ID != "dreamina-seedance-2-5-260628" {
		t.Fatal(entries, e)
	}
	fields := entries[0].OperationalFields
	var counts map[string]int
	if json.Unmarshal(fields["workflow.references.video.count"], &counts) != nil || counts["max"] != 10 {
		t.Fatal("2.5 inherited another model's video limit", fields)
	}
	var resolutions []string
	json.Unmarshal(fields["output.resolutions"], &resolutions)
	if strings.Contains(strings.Join(resolutions, ","), "4k") {
		t.Fatal("2.0 resolution leaked into 2.5")
	}
	var duration map[string]int
	json.Unmarshal(fields["output.duration"], &duration)
	if duration["min"] != 4 || duration["max"] != 30 || duration["default"] != -1 {
		t.Fatal(duration)
	}
	if entries[0].Contract != nil || len(entries[0].Rates) > 0 {
		t.Fatal("source changed protocol or tariff")
	}
	changed := strings.ReplaceAll(string(b), "reference videos (0–10)", "reference videos (unknown)")
	result, e := interpretVideoAPI(videoAPISource, changed)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := result[0].OperationalFields["workflow.references.video.count"]; ok {
		t.Fatal("missing count inferred from adjacent audio/model values")
	}
	if _, e = interpretVideoAPI(videoAPISource, "You need to enable JavaScript to run this app."); e == nil {
		t.Fatal("shell accepted")
	}
	if result, e := interpretVideoAPI(catalogsync.Source{URL: "https://docs.byteplus.com/en/docs/ModelArk/1520757"}, string(b)); e != nil || result != nil {
		t.Fatal("obsolete shell alias treated as registered full authority")
	}
}

func TestSeedanceReviewedFamilyRequiresExactDisplayToken(t *testing.T) {
	body, err := os.ReadFile("testdata/video-api/seedance-parameters.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"future-Dreamina Seedance 2.5", "Dreamina Seedance 2.50", "Dreamina Seedance 2.5-future"} {
		changed := strings.ReplaceAll(string(body), "Dreamina Seedance 2.5", family)
		if entries, err := interpretVideoAPI(videoAPISource, changed); err == nil || len(entries) != 0 {
			t.Fatal("unreviewed family mapped to adapter ID", family, entries, err)
		}
	}
}
