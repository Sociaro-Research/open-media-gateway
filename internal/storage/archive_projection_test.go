package storage

import (
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/diagnostics"
)

func TestHistoricalArchiveProjectionRecognizesOnlySafeForms(t *testing.T) {
	for _, raw := range []string{
		"provider result exceeds STORAGE_MAX_BYTES (1024)", oversizedResult(1024).Error(),
		"S3 upload failed: storage endpoint blocked by outbound private-address policy",
		"S3 read failed: storage endpoint hostname could not be resolved",
		"S3 upload failed: storage endpoint requires HTTPS for authenticated requests",
		PublicArchiveError(ErrEndpointHTTPSRequired),
	} {
		safe, ok := diagnostics.Project(raw)
		if !ok || !strings.Contains(safe, "operator") || !strings.Contains(safe, "original task") {
			t.Fatal("lost archive cause/action", safe)
		}
		if again, ok := diagnostics.Project(safe); !ok || again != safe {
			t.Fatal("projection is not idempotent")
		}
	}
	for _, raw := range []string{"provider result exceeds STORAGE_MAX_BYTES (01)", "provider result exceeds STORAGE_MAX_BYTES (-1)", "provider result exceeds STORAGE_MAX_BYTES (1024) /private/path", "S3 read failed: storage endpoint hostname could not be resolved private-token", "S3 upload failed: storage endpoint requires HTTPS for authenticated requests private-token", "authenticated outbound requests require HTTPS", "S3 upload failed; check endpoint, region, bucket and network connectivity private-token"} {
		if _, ok := diagnostics.Project(raw); ok {
			t.Fatal("accepted untrusted archive form", raw)
		}
	}
}
