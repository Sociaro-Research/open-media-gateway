package providerassets

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
)

// Explicit opt-in: public GET requests only, never provider account credentials.
func TestOfficialCatalogueSources(t *testing.T) {
	if os.Getenv("GATEWAY_CATALOG_PUBLIC_SOURCES") != "1" {
		t.Skip("live public source verification was not requested")
	}
	client := httpclient.NewPublic(30 * time.Second)
	for _, d := range All() {
		for _, source := range d.CatalogSources {
			t.Run(d.ID+"/"+source.ID, func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
				defer cancel()
				ob := catalogsync.Fetch(ctx, client, source)
				if ob.Error != "" {
					t.Fatal(ob.Error)
				}
				t.Logf("%d normalized records; snapshot %s", len(ob.Entries), ob.Hash)
			})
		}
	}
}
