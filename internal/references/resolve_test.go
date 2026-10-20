package references

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

const resolverOwner = "67440eea-86ed-49fe-a70a-353f305988dd"
const resolverID = "969eda6a-8f12-4b5b-b5f7-b505e3cea208"

type referenceReadCloser struct {
	*bytes.Reader
	closed *bool
}

func (r referenceReadCloser) Close() error { *r.closed = true; return nil }

type openerStub struct {
	owner        string
	calls        int
	data         []byte
	err          error
	closed       bool
	reportedSize int64
}

func (o *openerStub) Open(ctx context.Context, id string) (Image, storage.Object, error) {
	o.calls++
	if identity.OwnerID(ctx) != o.owner || id != resolverID {
		return Image{}, storage.Object{}, errors.New("wrong owner or reference")
	}
	size := int64(len(o.data))
	if o.reportedSize != 0 {
		size = o.reportedSize
	}
	return Image{SizeBytes: size, ContentType: "image/jpeg"}, storage.Object{Reader: referenceReadCloser{bytes.NewReader(o.data), &o.closed}}, o.err
}
func resolverURL(host string) string {
	return host + "/references/" + resolverOwner + "/" + resolverID + "?signature=" + Signature([]byte("secret"), resolverOwner, resolverID)
}

func TestResolveSignedReferenceWithoutHTTP(t *testing.T) {
	opener := &openerStub{owner: resolverOwner, data: []byte("exact original image bytes")}
	resolver := NewResolver(opener, "secret")
	// Even an unresolvable hostname cannot make the resolver issue an HTTP request.
	signed := resolverURL("https://unreachable.invalid")
	external := "https://external.example/image.jpg"
	input := []string{external, signed}
	resolved, err := resolver.Resolve(identity.WithOwnerID(context.Background(), resolverOwner), input)
	if err != nil {
		t.Fatal(err)
	}
	if resolved[0] != external || resolved[1] != "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(opener.data) {
		t.Fatal("incorrect ordered image contents")
	}
	if input[1] != signed {
		t.Fatal("mutated persisted task input")
	}
	if opener.calls != 1 || !opener.closed {
		t.Fatal("storage was not read and closed exactly once")
	}
}

func TestResolveRejectsCrossAccountAndLeavesUnsignedURLsAlone(t *testing.T) {
	o := &openerStub{owner: resolverOwner, data: []byte("image")}
	r := NewResolver(o, "secret")
	_, err := r.Resolve(identity.WithOwnerID(context.Background(), "other"), []string{resolverURL("https://gateway.example")})
	if err == nil || o.calls != 0 {
		t.Fatal("cross-account access was not rejected before storage")
	}
	raw := strings.Replace(resolverURL("https://gateway.example"), "signature=", "signature=invalid", 1)
	got, err := r.Resolve(context.Background(), []string{raw, "http://127.0.0.1/private"})
	if err != nil || got[0] != raw || o.calls != 0 {
		t.Fatal("unsigned URLs must never be fetched from storage or HTTP")
	}
}

func TestResolveStorageFailuresAndPayloadBudget(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int64
		err  error
	}{
		{"deleted", 0, storage.ErrObjectNotFound}, {"truncated", 100, nil}, {"oversize", MaxBytes + 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &openerStub{owner: resolverOwner, data: []byte("image"), reportedSize: tc.size, err: tc.err}
			_, err := NewResolver(o, "secret").Resolve(identity.WithOwnerID(context.Background(), resolverOwner), []string{resolverURL("https://gateway.example")})
			if err == nil {
				t.Fatal("invalid storage content accepted")
			}
		})
	}
	// Bounded encoded payload: subsequent large references retain their URLs.
	o := &openerStub{owner: resolverOwner, data: bytes.Repeat([]byte{1}, 20<<20)}
	raw := resolverURL("https://gateway.example")
	got, err := NewResolver(o, "secret").Resolve(identity.WithOwnerID(context.Background(), resolverOwner), []string{raw, raw})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got[0], "data:image/jpeg;base64,") || got[1] != raw {
		t.Fatal("inline budget not enforced")
	}
}

var _ io.ReadSeekCloser = referenceReadCloser{}

type directOpener struct{ openerStub }

func (o *directOpener) DirectReferenceURL(ctx context.Context, id string) (string, error) {
	if identity.OwnerID(ctx) != resolverOwner || id != resolverID {
		return "", errors.New("wrong owner")
	}
	return "https://bucket.example/direct?signature=s3", nil
}
func TestS3ReferencesStayDirectEvenWhenInlinePreferred(t *testing.T) {
	opener := &directOpener{}
	resolver := NewResolver(opener, "secret")
	ctx := identity.WithOwnerID(context.Background(), resolverOwner)
	result, err := resolver.Resolve(ctx, []string{resolverURL("https://gateway.example")})
	if err != nil || len(result) != 1 || result[0] != "https://bucket.example/direct?signature=s3" {
		t.Fatalf("direct reference: %v %v", result, err)
	}
	if opener.calls != 0 {
		t.Fatal("S3 reference was opened for inline conversion")
	}
	input := core.MediaOptions{ImageURLs: []string{resolverURL("")}, VideoURLs: []string{resolverURL("")}, AudioURLs: []string{resolverURL("")}, VideoURL: resolverURL(""), AudioURL: resolverURL(""), ReferenceVoices: map[string]string{resolverURL(""): resolverURL("")}}
	output, err := resolver.ResolveDirect(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	direct := result[0]
	if output.ImageURLs[0] != direct || output.VideoURLs[0] != direct || output.AudioURLs[0] != direct || output.VideoURL != direct || output.AudioURL != direct || output.ReferenceVoices[direct] != direct {
		t.Fatalf("reference type was not resolved: %+v", output)
	}
	if input.ImageURLs[0] != resolverURL("") || input.ReferenceVoices[resolverURL("")] != resolverURL("") {
		t.Fatal("mutated persisted references")
	}
	if opener.calls != 0 {
		t.Fatal("provider reference resolution transferred bytes")
	}
}
