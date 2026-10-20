package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type memoryS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
	methods []string
	ranges  []string
	deny    string
}

func newMemoryS3() *memoryS3 {
	return &memoryS3{objects: map[string][]byte{}, types: map[string]string{}}
}
func (m *memoryS3) RoundTrip(r *http.Request) (*http.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
		return nil, errors.New("missing SigV4 authorization")
	}
	m.methods = append(m.methods, r.Method)
	key := r.URL.Host + r.URL.Path
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "application/xml")
	if m.deny == r.Method {
		w.WriteHeader(403)
		fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>secret diagnostic</Message></Error>`)
		return w.Result(), nil
	}
	switch r.Method {
	case "PUT":
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) != r.ContentLength {
			return nil, fmt.Errorf("wrong content length %d vs %d", len(data), r.ContentLength)
		}
		m.objects[key] = data
		m.types[key] = r.Header.Get("Content-Type")
		w.Header().Set("ETag", `"test-etag"`)
	case "DELETE":
		delete(m.objects, key)
		delete(m.types, key)
		w.WriteHeader(204)
	case "GET", "HEAD":
		data, found := m.objects[key]
		if !found {
			w.WriteHeader(404)
			fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			break
		}
		w.Header().Set("Content-Type", m.types[key])
		w.Header().Set("ETag", `"test-etag"`)
		w.Header().Set("Last-Modified", "Sat, 05 Sep 2026 12:00:00 GMT")
		code := 200
		if raw := r.Header.Get("Range"); raw != "" {
			m.ranges = append(m.ranges, raw)
			var start int
			if _, err := fmt.Sscanf(raw, "bytes=%d-", &start); err != nil || start < 0 || start >= len(data) {
				return nil, errors.New("bad range")
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
			data = data[start:]
			code = 206
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.WriteHeader(code)
		if r.Method == "GET" {
			_, _ = w.Write(data)
		}
	default:
		return nil, errors.New("unexpected method")
	}
	return w.Result(), nil
}
func testS3Config() S3Config {
	return S3Config{Endpoint: "https://objects.example", Region: "auto", Bucket: "media-test", Prefix: "generated", PathStyle: true, AccessKeyID: "test-access", SecretAccessKey: "test-secret"}
}
func TestS3EndpointNormalization(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{" s3.us-west-004.backblazeb2.com/ ", "https://s3.us-west-004.backblazeb2.com"},
		{"objects.example:9000", "https://objects.example:9000"},
		{"//objects.example", "https://objects.example"},
		{"https://objects.example/", "https://objects.example"},
		{"http://localhost:9000/", "http://localhost:9000"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			c := testS3Config()
			c.Endpoint = tc.input
			if err := c.normalize(); err != nil || c.Endpoint != tc.want {
				t.Fatalf("endpoint = %q, error = %v; want %q", c.Endpoint, err, tc.want)
			}
		})
	}
	for _, endpoint := range []string{"ftp://objects.example", "https://", "https:/objects.example", "objects.example/path", "objects.example?query=yes", "objects.example#fragment", "user:password@objects.example", "not a host"} {
		t.Run(endpoint, func(t *testing.T) {
			c := testS3Config()
			c.Endpoint = endpoint
			if err := c.normalize(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("invalid endpoint accepted: %q, %v", endpoint, err)
			}
		})
	}
}
func TestS3ArchiveRangeHeadAndMissing(t *testing.T) {
	remote := newMemoryS3()
	s, err := NewS3(testS3Config(), &http.Client{Transport: remote}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	media, err := s.Archive(context.Background(), "task-1", "data:video/mp4;base64,c21hbGwtdmlkZW8=")
	if err != nil {
		t.Fatal(err)
	}
	if media.StorageKey != "generated/task-1/video.mp4" || media.SizeBytes != 11 || len(media.SHA256) != 64 {
		t.Fatalf("bad metadata: %+v", media)
	}
	object, err := s.Open(context.Background(), media.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	defer object.Reader.Close()
	if len(remote.methods) != 2 || remote.methods[1] != "HEAD" {
		t.Fatal("Open downloaded the object")
	}
	_, _ = object.Reader.Seek(6, io.SeekStart)
	tail, err := io.ReadAll(object.Reader)
	if err != nil || string(tail) != "video" {
		t.Fatalf("bad tail %q: %v", tail, err)
	}
	_, _ = object.Reader.Seek(0, io.SeekStart)
	body, err := io.ReadAll(object.Reader)
	if err != nil || string(body) != "small-video" {
		t.Fatalf("bad data %q: %v", body, err)
	}
	if strings.Join(remote.ranges, ",") != "bytes=6-,bytes=0-" {
		t.Fatal(remote.ranges)
	}
	if _, err := s.Open(context.Background(), "missing"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing object: %v", err)
	}
	if _, err := s.Archive(context.Background(), "../escape", "data:video/mp4;base64,YQ=="); err == nil {
		t.Fatal("accepted traversal")
	}
}
func TestS3ProbeCleanupAndNoSecretErrors(t *testing.T) {
	for _, deny := range []string{"", "GET", "PUT", "DELETE"} {
		t.Run("deny_"+deny, func(t *testing.T) {
			remote := newMemoryS3()
			remote.deny = deny
			s, _ := NewS3(testS3Config(), &http.Client{Transport: remote}, 1024)
			err := s.Check(context.Background())
			if (err != nil) != (deny != "") {
				t.Fatalf("unexpected check: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret diagnostic") {
				t.Fatal("leaked remote diagnostics")
			}
			if deny != "DELETE" && len(remote.objects) != 0 {
				t.Fatal("probe was not removed")
			}
		})
	}
}
func TestS3LimitsAndSourceValidation(t *testing.T) {
	remote := newMemoryS3()
	s, _ := NewS3(testS3Config(), &http.Client{Transport: remote}, 4)
	for _, source := range []string{"data:video/mp4;base64,dG9vLWxhcmdl", "data:text/html;base64,YQ==", "data:video/mp4;base64,", "file:///etc/passwd"} {
		if _, err := s.Archive(context.Background(), "task", source); err == nil {
			t.Fatalf("accepted %s", source)
		}
	}
	if len(remote.methods) != 0 {
		t.Fatal("invalid file uploaded")
	}
}
func TestS3VirtualHostedAddressing(t *testing.T) {
	remote := newMemoryS3()
	c := testS3Config()
	c.PathStyle = false
	s, _ := NewS3(c, &http.Client{Transport: remote}, 1024)
	_, err := s.Archive(context.Background(), "task", "data:image/png;base64,YQ==")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := remote.objects["media-test.objects.example/generated/task/image.png"]; !ok {
		t.Fatal("virtual hosted bucket not used")
	}
}
