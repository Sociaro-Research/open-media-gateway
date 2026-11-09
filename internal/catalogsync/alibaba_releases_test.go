package catalogsync

import (
	"strings"
	"testing"
)

func TestAlibabaDatedReleaseCardsPreserveEarliestDateAndExactModel(t *testing.T) {
	body := `<html><main><div class="rkb-update"><h3>2026-10-22</h3><div><h4>pixverse/pixverse-v6-t2v</h4><p>Public video model. Recommended replacement: qwen-image-future.</p><h4>qwen-image-new</h4><p>Public image model.</p></div></div><div class="rkb-update"><h3>2026-10-01</h3><h4>pixverse/pixverse-v6-t2v</h4><p>First regional release.</p></div></main></html>`
	entries, _, err := Parse(Source{Parser: "alibaba-releases"}, []byte(body))
	if err != nil || len(entries) != 2 {
		t.Fatal(entries, err)
	}
	if entries[0].ID != "pixverse/pixverse-v6-t2v" || entries[0].Metadata.ReleaseDate != "2026-10-01" || entries[1].ID != "qwen-image-new" || entries[1].Metadata.ReleaseDate != "2026-10-22" {
		t.Fatal(entries)
	}
	if _, _, err := Parse(Source{Parser: "alibaba-releases"}, []byte(strings.Replace(body, "2026-10-22", "2026-02-30", 1))); err == nil {
		t.Fatal("invalid release date accepted")
	}
}
