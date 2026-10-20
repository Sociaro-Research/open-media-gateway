package catalogsync

import (
	"os"
	"path/filepath"
	"testing"
)

// Optional acceptance against read-only snapshots of complete official pages.
func TestAlibabaDownloadedSources(t *testing.T) {
	dir := os.Getenv("GATEWAY_ALIBABA_REVIEW_DIR")
	if dir == "" {
		t.Skip("no downloaded Alibaba source snapshots")
	}
	for _, tc := range []struct{ name, parser string }{{"model-pricing", "alibaba-pricing"}, {"newly-released-models", "alibaba-releases"}, {"retirement", "alibaba-retirements"}, {"image-model", "alibaba-image-models"}} {
		data, err := os.ReadFile(filepath.Join(dir, tc.name+".html"))
		if err != nil {
			t.Fatal(err)
		}
		entries, _, err := Parse(Source{Parser: tc.parser}, data)
		if err != nil {
			t.Fatal(tc.name, err)
		}
		t.Log(tc.name, "entries", len(entries))
		if err = os.WriteFile(filepath.Join(dir, tc.name+"-parsed.json"), encoded(entries), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
