package storage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/google/uuid"
)

type multipartS3 struct {
	mu                          sync.Mutex
	requests                    []string
	size                        int64
	digest                      string
	badSize, badHash, completed bool
	omitChecksum                bool
}

func (m *multipartS3) RoundTrip(r *http.Request) (*http.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := r.URL.Query()
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "application/xml")
	m.requests = append(m.requests, r.Method+" "+r.URL.RequestURI())
	switch {
	case r.Method == "POST" && q.Has("uploads"):
		if r.Header.Get("X-Amz-Checksum-Algorithm") != "" {
			return nil, fmt.Errorf("optional multipart checksum extension is incompatible with R2")
		}
		m.digest = r.Header.Get("X-Amz-Meta-Sha256")
		fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>upload-1</UploadId></InitiateMultipartUploadResult>`)
	case r.Method == "GET" && q.Get("uploadId") != "":
		if m.completed {
			w.WriteHeader(404)
			fmt.Fprint(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			break
		}
		size := m.size
		if m.badSize {
			size++
		}
		digest, _ := hex.DecodeString(m.digest)
		if m.badHash {
			digest = []byte("wrong")
		}
		encoded := base64.StdEncoding.EncodeToString(digest)
		if m.omitChecksum {
			encoded = ""
		}
		fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><Size>%d</Size><ETag>"part-etag"</ETag><ChecksumSHA256>%s</ChecksumSHA256></Part></ListPartsResult>`, size, encoded)
	case r.Method == "POST" && q.Get("uploadId") == "upload-1":
		body, _ := io.ReadAll(r.Body)
		if (!m.omitChecksum && !strings.Contains(string(body), "ChecksumSHA256")) || !strings.Contains(string(body), "part-etag") {
			return nil, fmt.Errorf("missing pinned part metadata")
		}
		m.completed = true
		fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>"done"</ETag></CompleteMultipartUploadResult>`)
	case r.Method == "HEAD":
		w.Header().Set("Content-Length", fmt.Sprint(m.size))
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("X-Amz-Meta-Sha256", m.digest)
	case r.Method == "GET" && q.Has("uploads"):
		fmt.Fprint(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
	default:
		return nil, fmt.Errorf("unexpected S3 byte transfer: %s", r.Method+" "+r.URL.String())
	}
	return w.Result(), nil
}
func directManager(t *testing.T) (*Manager, *multipartS3, context.Context) {
	t.Helper()
	remote := &multipartS3{size: 1024}
	cipher, _ := providerconfig.NewCipher("test-direct-storage")
	m, err := NewManager(newSettingsMemory(), cipher, t.TempDir(), &http.Client{Transport: remote}, 200<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	r, _, err := m.prepare(ctx, s3Update())
	if err != nil {
		t.Fatal(err)
	}
	if err = m.repo.Save(ctx, r); err != nil {
		t.Fatal(err)
	}
	return m, remote, ctx
}
func TestDirectUploadAndReadNeverTransferObjectBytes(t *testing.T) {
	m, remote, ctx := directManager(t)
	sum := sha256.Sum256([]byte("test content"))
	digest := hex.EncodeToString(sum[:])
	pending, err := m.PlanUpload(ctx, uuid.NewString(), "image/png", digest, remote.size)
	if err != nil {
		t.Fatal(err)
	}
	if len(remote.requests) != 0 {
		t.Fatal("planning contacted S3")
	}
	target, err := m.StartUpload(ctx, pending)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(target.URL)
	q := u.Query()
	if q.Get("uploadId") != "upload-1" || q.Get("partNumber") != "1" || !strings.Contains(q.Get("X-Amz-SignedHeaders"), "content-length") {
		t.Fatalf("upload ID, part or size not signed: %s", target.URL)
	}
	if target.Headers["X-Amz-Content-Sha256"] != digest || !strings.Contains(q.Get("X-Amz-SignedHeaders"), "x-amz-content-sha256") || q.Has("X-Amz-Content-Sha256") {
		t.Fatalf("checksum not pinned: %v %s", target.Headers, target.URL)
	}
	if len(remote.requests) != 1 {
		t.Fatalf("presigning transmitted bytes: %v", remote.requests)
	}
	// A new connection selection must not change the in-flight upload destination.
	if _, err = m.Update(ctx, Update{Driver: "local"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		media, err := m.CompleteUpload(ctx, *pending)
		if err != nil || media.StorageKey != pending.Key {
			t.Fatalf("completion/retry: %+v %v", media, err)
		}
	}
	before := len(remote.requests)
	for _, method := range []string{"GET", "HEAD"} {
		location, err := m.DirectURL(ctx, pending.Key, method, "image/png", "attachment", 15*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(location)
		if parsed.Host != "objects.example" || parsed.Query().Get("X-Amz-Expires") != "900" {
			t.Fatal(location)
		}
	}
	if len(remote.requests) != before {
		t.Fatal("read signing opened object")
	}
	other := identity.WithOwnerID(ctx, uuid.NewString())
	if _, err = m.DirectURL(other, pending.Key, "GET", "", "", time.Minute); err == nil {
		t.Fatal("cross-owner read allowed")
	}
	if location, err := m.DirectURL(ctx, "local/key", "GET", "", "", time.Minute); err != nil || location != "" {
		t.Fatal("local storage must remain local")
	}
}
func TestDirectCompletionRejectsWrongSizeOrHash(t *testing.T) {
	for _, bad := range []string{"size", "checksum"} {
		t.Run(bad, func(t *testing.T) {
			m, remote, ctx := directManager(t)
			sum := sha256.Sum256([]byte("test"))
			pending, err := m.PlanUpload(ctx, uuid.NewString(), "image/png", hex.EncodeToString(sum[:]), remote.size)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.StartUpload(ctx, pending); err != nil {
				t.Fatal(err)
			}
			remote.badSize = bad == "size"
			remote.badHash = bad == "checksum"
			if _, err = m.CompleteUpload(ctx, *pending); err == nil {
				t.Fatal("invalid upload accepted")
			}
			if remote.completed {
				t.Fatal("invalid part completed")
			}
		})
	}
}

func TestDirectUploadWithS3WithoutOptionalChecksumExtension(t *testing.T) {
	m, remote, ctx := directManager(t)
	remote.omitChecksum = true
	sum := sha256.Sum256([]byte("test"))
	pending, err := m.PlanUpload(ctx, uuid.NewString(), "image/png", hex.EncodeToString(sum[:]), remote.size)
	if err != nil {
		t.Fatal(err)
	}
	target, err := m.StartUpload(ctx, pending)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(target.URL)
	if target.Headers["X-Amz-Content-Sha256"] != pending.SHA256 || !strings.Contains(u.Query().Get("X-Amz-SignedHeaders"), "x-amz-content-sha256") {
		t.Fatal("payload digest must remain mandatory and signed")
	}
	if _, err = m.CompleteUpload(ctx, *pending); err != nil {
		t.Fatal(err)
	}
}

func TestB2UploadUsesPayloadSignedAuthorization(t *testing.T) {
	m, remote, ctx := directManager(t)
	remote.size = 2 << 20 // Exercise the SDK's large-part Expect header.
	input := s3Update()
	input.Endpoint, input.Region = "https://s3.eu-central-003.backblazeb2.com", "eu-central-003"
	record, _, err := m.prepare(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.repo.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", int(remote.size))
	sum := sha256.Sum256([]byte(payload))
	pending, err := m.PlanUpload(ctx, uuid.NewString(), "image/png", hex.EncodeToString(sum[:]), remote.size)
	if err != nil {
		t.Fatal(err)
	}
	target, err := m.StartUpload(ctx, pending)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(target.URL)
	if u.Query().Get("uploadId") != "upload-1" || u.Query().Has("X-Amz-Signature") || u.Query().Has("X-Amz-Content-Sha256") {
		t.Fatal("B2 must use payload-signed headers on the pinned multipart target")
	}
	authorization := target.Headers["Authorization"]
	if target.Headers["Expect"] != "" {
		t.Fatal("browser target contains the forbidden Expect header")
	}
	if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 ") || !strings.Contains(authorization, "SignedHeaders=content-length;host;x-amz-content-sha256;x-amz-date") || target.Headers["X-Amz-Content-Sha256"] != pending.SHA256 {
		t.Fatal("B2 payload digest, exact size or signing time not bound")
	}
	signedAt, err := time.Parse("20060102T150405Z", target.Headers["X-Amz-Date"])
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []bool{false, true} {
		body := payload
		if changed {
			body = "y" + body[1:]
		}
		digest := sha256.Sum256([]byte(body))
		hash := hex.EncodeToString(digest[:])
		req, _ := http.NewRequest("PUT", target.URL, strings.NewReader(body))
		req.Header.Set("X-Amz-Content-Sha256", hash)
		err := v4.NewSigner().SignHTTP(ctx, aws.Credentials{AccessKeyID: input.AccessKeyID, SecretAccessKey: input.SecretAccessKey}, req, hash, "s3", input.Region, signedAt, func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
		if err != nil {
			t.Fatal(err)
		}
		if (req.Header.Get("Authorization") == authorization) == changed {
			t.Fatal("B2 authorization is not bound to the payload digest")
		}
	}
}
