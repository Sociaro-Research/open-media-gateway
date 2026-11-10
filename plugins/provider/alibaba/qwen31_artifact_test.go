package alibaba

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func TestQwen31ArtifactRejectsForeignStaleTamperedAndExpiredBeforeHTTP(t *testing.T) {
	calls := 0
	a, err := New("fixture-key", qwen31TestEndpoint, "", "", &http.Client{Transport: dashRoundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, io.ErrUnexpectedEOF })})
	if err != nil {
		t.Fatal(err)
	}
	ctx := core.WithProviderRevision(identity.WithResourceOwners(identity.WithOwnerID(context.Background(), "task-owner"), "shared-provider-owner", "store"), "revision-1")
	ctx = core.WithProviderConnectionScope(ctx, core.ProviderConnectionScope{ID: "connection-1", OwnerID: "shared-provider-owner", Revision: "actual-revision"})
	result, err := qwen31Result(qwen31TestRequest(qwen31Next), qwen31TestResponse(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := a.sealQwen31Result(ctx, result, time.Now().Add(time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	handle := accepted.Artifacts[0].SourceReference
	foreign := identity.WithOwnerID(ctx, "another-task-owner")
	stale := core.WithProviderRevision(ctx, "revision-2")
	wrongConnection := core.WithProviderConnectionScope(ctx, core.ProviderConnectionScope{ID: "connection-2", OwnerID: "shared-provider-owner", Revision: "actual-revision"})
	expired, err := a.sealQwen31Result(ctx, result, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ctx    context.Context
		handle string
	}{{foreign, handle}, {stale, handle}, {wrongConnection, handle}, {ctx, handle + "tampered"}, {ctx, expired.Artifacts[0].SourceReference}, {ctx, strings.Repeat("x", 48001)}} {
		if _, err := a.OpenArtifact(tc.ctx, tc.handle); err == nil || calls != 0 {
			t.Fatal("untrusted source reached HTTP", calls, err)
		}
	}
}

func TestQwen31ArtifactRejectsUnsafeRedirectsBeforeFollowing(t *testing.T) {
	for _, target := range []string{"http://media.example.test/result.wav", "https://127.0.0.1/result.wav", "https://localhost/result.wav", "https://private.internal/result.wav", "https://user:secret@media.example.test/result.wav"} {
		t.Run(target, func(t *testing.T) {
			calls := 0
			a, err := New("fixture-key", qwen31TestEndpoint, "", "", &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls != 1 || r.URL.Host != "media.example.test" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Fatal("unsafe redirect reached transport")
				}
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": {target}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			result, err := qwen31Result(qwen31TestRequest(qwen31Next), qwen31TestResponse(), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := a.sealQwen31Result(ctx, result, time.Now().Add(time.Hour).Unix())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.OpenArtifact(ctx, accepted.Artifacts[0].SourceReference); err == nil || calls != 1 {
				t.Fatal("unsafe redirect accepted", calls, err)
			}
		})
	}
}
