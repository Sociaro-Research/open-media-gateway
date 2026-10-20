package modelcatalog

import (
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"net/url"
	"testing"
	"time"
)

func TestCatalogProvenanceAndDates(t *testing.T) {
	counts := map[string]int{"openai": 29, "alibaba": 116, "bytedance": 17}
	for provider, models := range catalog {
		if len(models) != counts[provider] {
			t.Fatalf("%s coverage: %d", provider, len(models))
		}
		for id, m := range models {
			if m.Description == "" || m.CheckedAt == "" || len(m.Sources) == 0 {
				t.Errorf("%s/%s missing provenance", provider, id)
			}
			for _, d := range []string{m.ReleaseDate, m.VersionDate, m.ShutdownDate, m.CheckedAt} {
				if d == "" {
					continue
				}
				layout := "2006-01-02"
				if len(d) == 7 {
					layout = "2006-01"
				}
				if _, err := time.Parse(layout, d); err != nil {
					t.Errorf("%s invalid date %s", id, d)
				}
			}
			for _, s := range m.Sources {
				u, err := url.Parse(s.URL)
				if err != nil || u.Scheme != "https" || u.Host == "" {
					t.Errorf("%s invalid source", id)
				}
			}
		}
	}
}

func TestEnrichmentDoesNotLeakAccountMetadata(t *testing.T) {
	base := core.MediaModel{ID: "gpt-image-2.5-flare"}
	a := Enrich("openai", base)
	a.Metadata.Facts[0].Value = "changed"
	b := Enrich("openai", core.MediaModel{ID: base.ID, Metadata: &core.ModelMetadata{APICreated: 123, OwnedBy: "account fixture"}})
	c := Enrich("openai", base)
	if b.Metadata.Facts[0].Value == "changed" || c.Metadata.OwnedBy != "" || b.Metadata.APICreated != 123 {
		t.Fatal("catalogue metadata leaked")
	}
	if c.CatalogDate() != "2026-09-08" {
		t.Fatal("release date missing")
	}
	unknown := Enrich("openai", core.MediaModel{ID: "gpt-image-future"})
	if unknown.Metadata != nil {
		t.Fatal("invented metadata for unknown version")
	}
}
