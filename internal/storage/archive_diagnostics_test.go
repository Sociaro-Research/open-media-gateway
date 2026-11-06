package storage

import (
	"context"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/soundoverride"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/aws/smithy-go"
)

func TestPublicArchiveErrorKeepsOnlySafeCauses(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"sound override", fmt.Errorf("private-wrapper: %w", soundoverride.ErrProcessing), "worker FFmpeg installation"},
		{"unknown", errors.New("private-plugin https://private.invalid?token=private-token"), "check result storage"},
		{"filesystem", &os.PathError{Op: "mkdir", Path: "/private-root/private-token", Err: syscall.ENOTDIR}, "check result storage"},
		{"permissions", &os.PathError{Op: "write", Path: "/private-root", Err: os.ErrPermission}, "write permissions"},
		{"disk full", &os.PathError{Op: "write", Path: "/private-root", Err: syscall.ENOSPC}, "expand the configured storage"},
		{"size", oversizedResult(1024), "1024 bytes"},
		{"S3 permissions", safeS3Error("upload", &smithy.GenericAPIError{Code: "AccessDenied", Message: "private-provider-body"}), "bucket permissions"},
		{"S3 unknown", safeS3Error("upload", &smithy.GenericAPIError{Code: "private-code", Message: "private-provider-body"}), "endpoint, region, bucket"},
		{"private endpoint", fmt.Errorf("private-network: %w", ErrPrivateEndpoint), "allowlist"},
		{"DNS", fmt.Errorf("private-network: %w", ErrEndpointDNS), "DNS and connectivity"},
		{"HTTPS", fmt.Errorf("private-network: %w", ErrEndpointHTTPSRequired), "configure HTTPS or review an exact trusted storage host exception"},
		{"download", sourceOpenError{message: "download provider result failed", cause: errors.New("private-signed-url")}, "provider download availability"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := PublicArchiveError(tc.err)
			for _, private := range []string{"private-", "private.invalid", "private-code"} {
				if strings.Contains(message, private) {
					t.Fatal("private archive diagnostic escaped")
				}
			}
			if !strings.Contains(message, tc.want) || !strings.Contains(message, "original task before creating another paid generation") {
				t.Fatal("safe diagnosis/action missing", message)
			}
		})
	}
}

func TestPublicArchiveErrorPreservesInspectedArtifactFailures(t *testing.T) {
	local, err := NewLocal(t.TempDir(), http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, kind, body, want string }{
		{"JSON", "application/json", "{private-body", "contains invalid JSON"},
		{"MIME", "image/png", "<html>private-body</html>", "does not match its declared content type"},
		{"type", "application/x-private", "private-body", "unsupported content type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := local.ArchiveSource(context.Background(), "fixture", core.ArtifactSource{Reader: io.NopCloser(strings.NewReader(tc.body)), ContentType: tc.kind, SizeBytes: -1})
			if err == nil {
				t.Fatal("invalid stream accepted")
			}
			message := PublicArchiveError(err)
			if !strings.Contains(message, tc.want) || strings.Contains(message, "private-") {
				t.Fatal("lost safe inspected reason", message)
			}
		})
	}
}
