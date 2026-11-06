package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

func TestStorageCheckReportsPrivateEndpointPolicy(t *testing.T) {
	var hits atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(500) }))
	defer endpoint.Close()
	store, err := storage.NewS3(storage.S3Config{Endpoint: endpoint.URL, Region: "us-east-1", Bucket: "fixture-bucket", AccessKeyID: "fixture-access", SecretAccessKey: "fixture-secret"}, httpclient.NewPublic(time.Second), 1024)
	if err != nil {
		t.Fatal(err)
	}
	probeErr := store.Check(context.Background())
	if probeErr == nil || hits.Load() != 0 {
		t.Fatal("private endpoint reached the server")
	}
	s, _ := newPersonalTestServer()
	s.storageAdmin = &storageAdminStub{checkErr: probeErr}
	response := sendTest(s.handler(), "POST", "/v1/admin/storage/check", `{"driver":"s3"}`, "alice", "")
	if response.Code != 400 || !strings.Contains(response.Body.String(), `"storage_endpoint_blocked"`) || !strings.Contains(response.Body.String(), "OUTBOUND_ALLOW_PRIVATE_HOSTS") {
		t.Fatalf("missing safe policy diagnosis: %d %s", response.Code, response.Body.String())
	}
	for _, secret := range []string{endpoint.URL, "fixture-access", "fixture-secret"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("diagnostic leaked request details")
		}
	}
}

func TestStorageCheckClassifiedMessagesAreBounded(t *testing.T) {
	for _, tc := range []struct {
		name           string
		cause          error
		code, fragment string
	}{
		{"policy", storage.ErrPrivateEndpoint, "storage_endpoint_blocked", "OUTBOUND_ALLOW_PRIVATE_HOSTS"},
		{"DNS", storage.ErrEndpointDNS, "storage_endpoint_dns_failed", "hostname could not be resolved"},
		{"HTTPS", storage.ErrEndpointHTTPSRequired, "storage_endpoint_https_required", "Configure an HTTPS S3 endpoint"},
		{"fallback", errors.New("fixture-secret raw response"), "storage_check_failed", "Verify endpoint, region, bucket, credentials"},
		{"untyped HTTPS", errors.New("authenticated outbound requests require HTTPS fixture-secret"), "storage_check_failed", "Verify endpoint, region, bucket, credentials"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newPersonalTestServer()
			s.storageAdmin = &storageAdminStub{checkErr: fmt.Errorf("fixture-secret signed URL response body: %w", tc.cause)}
			w := sendTest(s.handler(), "POST", "/v1/admin/storage/check", `{"driver":"s3"}`, "alice", "")
			body := w.Body.String()
			if w.Code != 400 || !strings.Contains(body, `"`+tc.code+`"`) || !strings.Contains(body, tc.fragment) {
				t.Fatalf("wrong mapping: %d %s", w.Code, body)
			}
			for _, raw := range []string{"fixture-secret", "signed URL", "response body"} {
				if strings.Contains(body, raw) {
					t.Fatal("raw error escaped API boundary")
				}
			}
		})
	}
}

type storageCheckDNSFailureTransport struct{}

func (storageCheckDNSFailureTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("fixture-secret https://signed.example/?token=private: %w", httpclient.ErrOutboundDNS)
}
func TestStorageCheckReportsDNSCauseThroughSDK(t *testing.T) {
	store, err := storage.NewS3(storage.S3Config{Endpoint: "https://fixture.invalid", Region: "us-east-1", Bucket: "fixture-bucket", AccessKeyID: "fixture-access", SecretAccessKey: "fixture-secret"}, &http.Client{Transport: storageCheckDNSFailureTransport{}}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	probeErr := store.Check(context.Background())
	if !errors.Is(probeErr, storage.ErrEndpointDNS) {
		t.Fatalf("SDK lost classified DNS cause: %v", probeErr)
	}
	s, _ := newPersonalTestServer()
	s.storageAdmin = &storageAdminStub{checkErr: probeErr}
	w := sendTest(s.handler(), "POST", "/v1/admin/storage/check", `{"driver":"s3"}`, "alice", "")
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"storage_endpoint_dns_failed"`) {
		t.Fatalf("wrong DNS diagnosis: %d %s", w.Code, w.Body.String())
	}
	for _, secret := range []string{"fixture-secret", "fixture-access", "fixture.invalid", "signed.example", "token=private"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("SDK details leaked")
		}
	}
}

type storageCheckHTTPSFailureTransport struct{ calls atomic.Int32 }

func (tr *storageCheckHTTPSFailureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.calls.Add(1)
	if r.URL.Scheme != "http" || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
		return nil, errors.New("expected synthetic signed HTTP S3 request")
	}
	return nil, fmt.Errorf("fixture-secret http://signed.example/?token=private: %w", httpclient.ErrOutboundHTTPSRequired)
}

func TestStorageCheckReportsHTTPSCauseThroughSDK(t *testing.T) {
	// The transport is synthetic: no DNS, socket or external endpoint is used.
	transport := &storageCheckHTTPSFailureTransport{}
	store, err := storage.NewS3(storage.S3Config{Endpoint: "http://fixture.invalid", Region: "us-east-1", Bucket: "fixture-bucket", PathStyle: true, AccessKeyID: "fixture-access", SecretAccessKey: "fixture-secret"}, &http.Client{Transport: transport}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	probeErr := store.Check(context.Background())
	if !errors.Is(probeErr, storage.ErrEndpointHTTPSRequired) || transport.calls.Load() == 0 {
		t.Fatalf("SDK lost HTTPS policy cause: %v", probeErr)
	}
	if strings.Contains(probeErr.Error(), "fixture-secret") || strings.Contains(probeErr.Error(), "signed.example") {
		t.Fatal("storage retained untrusted SDK details")
	}
	s, _ := newPersonalTestServer()
	admin := &storageAdminStub{checkErr: probeErr}
	s.storageAdmin = admin
	h := s.handler()
	_, basic := issueTestKey(t, h, "alice", `{"name":"Generation only"}`)
	for _, auth := range []struct {
		session, key string
		status       int
	}{{"", "", 401}, {"", basic, 403}} {
		w := sendTest(h, "POST", "/v1/admin/storage/check", `{"driver":"s3"}`, auth.session, auth.key)
		if w.Code != auth.status || admin.checks != 0 {
			t.Fatal("storage policy diagnosis bypassed authentication or scope")
		}
	}
	w := sendTest(h, "POST", "/v1/admin/storage/check", `{"driver":"s3"}`, "alice", "")
	body := w.Body.String()
	if w.Code != 400 || !strings.Contains(body, `"storage_endpoint_https_required"`) || !strings.Contains(body, "HTTPS S3 endpoint") || !strings.Contains(body, "OUTBOUND_ALLOW_PRIVATE_HOSTS") || admin.checks != 1 {
		t.Fatalf("wrong HTTPS policy diagnosis: %d %s", w.Code, body)
	}
	for _, secret := range []string{"fixture-secret", "fixture-access", "fixture.invalid", "signed.example", "token=private", "AWS4-HMAC-SHA256"} {
		if strings.Contains(body, secret) {
			t.Fatal("SDK request details leaked")
		}
	}
}
