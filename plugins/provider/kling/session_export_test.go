package kling

import (
	"context"
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// SessionOpenTrackingForTest keeps acceptance tests on the real authenticated
// source opener while injecting a deterministic CDN transport, never a global.
func SessionOpenTrackingForTest(a *Adapter, ctx context.Context, reference string, client *http.Client) (core.ArtifactSource, error) {
	return a.openTrackingArtifact(ctx, reference, client)
}
