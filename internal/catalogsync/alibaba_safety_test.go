package catalogsync

import (
	"strings"
	"testing"
)

func TestAlibabaCapabilityParsersRejectEmptyTableHeaders(t *testing.T) {
	for _, parser := range []string{"alibaba-image-models", "alibaba-retirements"} {
		for name, table := range map[string]string{
			"no rows":             "<table></table>",
			"empty first row":     "<table><tr></tr><tr><td>x</td></tr></table>",
			"empty rows":          "<table><tr></tr><tr></tr></table>",
			"unrecognized header": "<table><tr><th>Other</th></tr><tr><td>x</td></tr></table>",
		} {
			t.Run(parser+"/"+name, func(t *testing.T) {
				defer func() {
					if value := recover(); value != nil {
						t.Errorf("malformed source table panicked: %v", value)
					}
				}()
				entries, _, err := Parse(Source{Parser: parser}, []byte("<p>Affected time 2026-10-20</p>"+table))
				if err == nil || len(entries) != 0 {
					t.Fatalf("unrecognized table accepted: entries=%v err=%v", entries, err)
				}
			})
		}
	}
}

func TestAlibabaUnknownBillingFormatsRemainReviewable(t *testing.T) {
	for _, table := range []string{
		`<tr><th>Model ID</th><th>Input price (USD/million tokens)</th><th>Output price (USD/million tokens)</th></tr><tr><td>qwen-audio-9.0-tts-next</td><td>0.848</td><td>1.696</td></tr>`,
		`<tr><th>Model ID</th><th>Output price</th><th>New billing condition</th></tr><tr><td>wan9.0-t2v</td><td>$ 0.26/second</td><td>Depends on scene count</td></tr>`,
	} {
		entries, text, err := Parse(Source{Parser: "alibaba-pricing"}, []byte("<h2>China (Beijing)</h2><table>"+table+"</table>"))
		if err != nil || len(entries) != 1 || len(entries[0].Rates) != 0 || entries[0].Metadata == nil || len(entries[0].Metadata.Facts) != 1 {
			t.Fatal("unknown billing terms were discarded or interpreted as a scalar price", entries, err)
		}
		if !strings.Contains(text, entries[0].ID) || !strings.Contains(text, entries[0].Metadata.Facts[0].Value) {
			t.Fatal("source evidence missing", text)
		}
	}
}
