package storage

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/aws/smithy-go"
)

func TestSafeS3ErrorPreservesOnlyClassifiedOutboundCauses(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cause, want error
	}{
		{"policy", httpclient.ErrOutboundPrivateDestination, ErrPrivateEndpoint},
		{"DNS", httpclient.ErrOutboundDNS, ErrEndpointDNS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Model the SDK operation wrapper and untrusted transport diagnostics.
			raw := &smithy.OperationError{ServiceID: "S3", OperationName: "PutObject", Err: fmt.Errorf("fixture-secret https://signed.example/?token=private: %w", tc.cause)}
			got := safeS3Error("write check", raw)
			if !errors.Is(got, tc.want) {
				t.Fatalf("safe classification lost: %v", got)
			}
			for _, secret := range []string{"fixture-secret", "signed.example", "token=private"} {
				if strings.Contains(got.Error(), secret) {
					t.Fatal("unsafe diagnostic escaped")
				}
			}
		})
	}
}

func TestSafeS3ErrorKeepsServiceAndFallbackBehavior(t *testing.T) {
	for _, tc := range []struct {
		code, want string
		missing    bool
	}{
		{"NoSuchKey", "", true}, {"NotFound", "", true},
		{"AccessDenied", "S3 write check denied; check credentials and bucket permissions", false},
		{"InvalidAccessKeyId", "S3 write check denied; check credentials and bucket permissions", false},
		{"SignatureDoesNotMatch", "S3 write check denied; check credentials and bucket permissions", false},
		{"InternalError", "S3 write check failed; check endpoint, region, bucket and network connectivity", false},
	} {
		t.Run(tc.code, func(t *testing.T) {
			got := safeS3Error("write check", &smithy.GenericAPIError{Code: tc.code, Message: "fixture-secret raw response"})
			if tc.missing {
				if !errors.Is(got, ErrObjectNotFound) {
					t.Fatal(got)
				}
			} else if got.Error() != tc.want {
				t.Fatalf("compatibility: %v", got)
			}
		})
	}
	got := safeS3Error("write check", &net.DNSError{Name: "fixture-secret", Err: "raw transport detail"})
	if got.Error() != "S3 write check failed; check endpoint, region, bucket and network connectivity" {
		t.Fatal("raw DNS fallback changed", got)
	}
}
