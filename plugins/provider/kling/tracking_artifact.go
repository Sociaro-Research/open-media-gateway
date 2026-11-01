package kling

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/Sociaro-Research/open-media-gateway/internal/webhookurl"
)

const trackingReferencePrefix = "kling-tracking-v1."
const maxTrackingArtifactBytes int64 = 32 << 20

// Tracking output has no published serialization contract. Preserve its bytes
// as an opaque attachment; never pretend it is a schema-validated JSON result.
var trackingArtifactClient = httpclient.NewPublic(30 * time.Second)

func trackingURL(raw string) bool {
	u, err := url.Parse(raw)
	return len(raw) > 0 && len(raw) <= 4096 && err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == "" && webhookurl.Validate(raw) == nil
}
func (a *Adapter) trackingCipher() (cipher.AEAD, error) {
	mac := hmac.New(sha256.New, []byte(a.apiKey))
	_, _ = mac.Write([]byte("kling-private-tracking-source-v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, errors.New("Kling tracking artifact could not be protected")
	}
	return cipher.NewGCM(block)
}

// Only a native response normalizer calls this helper. The URL is encrypted and
// authenticated together with the actual host connection/revision/data owner;
// neither an arbitrary client host nor raw provider credentials enter delivery.
func (a *Adapter) privateSourceReference(ctx context.Context, rawURL string) (string, error) {
	scope, err := legacyScope(ctx)
	if err != nil {
		return "", err
	}
	if !trackingURL(rawURL) {
		return "", errors.New("Kling tracking artifact source is invalid")
	}
	sealed, err := a.trackingCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, sealed.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", errors.New("Kling tracking artifact could not be protected")
	}
	payload := sealed.Seal(nonce, nonce, []byte(rawURL), []byte(trackingReferencePrefix+scope))
	return trackingReferencePrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}
func (a *Adapter) createTrackingArtifact(ctx context.Context, rawURL string) (core.ProviderArtifact, error) {
	reference, err := a.privateSourceReference(ctx, rawURL)
	if err != nil {
		return core.ProviderArtifact{}, err
	}
	return core.ProviderArtifact{ID: "selection_tracking", MediaType: "binary", Format: "bin", SourceReference: reference}, nil
}
func (a *Adapter) trackingSourceURL(ctx context.Context, reference string) (string, error) {
	invalid := errors.New("invalid Kling tracking artifact for this owner, connection or revision")
	scope, err := legacyScope(ctx)
	if err != nil {
		return "", invalid
	}
	if !strings.HasPrefix(reference, trackingReferencePrefix) || len(reference) > 8192 {
		return "", invalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(reference, trackingReferencePrefix))
	if err != nil {
		return "", invalid
	}
	sealed, err := a.trackingCipher()
	if err != nil || len(payload) < sealed.NonceSize()+sealed.Overhead() {
		return "", invalid
	}
	nonce := payload[:sealed.NonceSize()]
	plain, err := sealed.Open(nil, nonce, payload[sealed.NonceSize():], []byte(trackingReferencePrefix+scope))
	if err != nil || !trackingURL(string(plain)) {
		return "", invalid
	}
	return string(plain), nil
}

// OpenArtifact intentionally uses a separate public-network client. A download
// never inherits API authorization, cookies or provider-configured destinations.
func (a *Adapter) OpenArtifact(ctx context.Context, reference string) (core.ArtifactSource, error) {
	return a.openTrackingArtifact(ctx, reference, trackingArtifactClient)
}
func (a *Adapter) openTrackingArtifact(ctx context.Context, reference string, client *http.Client) (core.ArtifactSource, error) {
	rawURL, err := a.trackingSourceURL(ctx, reference)
	if err != nil {
		return core.ArtifactSource{}, err
	}
	if err := ctx.Err(); err != nil {
		return core.ArtifactSource{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return core.ArtifactSource{}, errors.New("Kling tracking artifact could not be opened")
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "Open-Media-Gateway/Kling-Tracking-Artifact")
	copyClient := *client
	copyClient.Jar = nil
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if ctx.Err() != nil {
			return core.ArtifactSource{}, ctx.Err()
		}
		return core.ArtifactSource{}, errors.New("Kling tracking artifact download failed")
	}
	if response.Body == nil {
		return core.ArtifactSource{}, errors.New("Kling tracking artifact has no content")
	}
	if response.StatusCode != http.StatusOK || response.ContentLength > maxTrackingArtifactBytes || response.ContentLength < -1 {
		_ = response.Body.Close()
		return core.ArtifactSource{}, errors.New("Kling tracking artifact response is invalid or exceeds its size limit")
	}
	return core.ArtifactSource{Reader: &trackingArtifactBody{body: response.Body, remaining: maxTrackingArtifactBytes}, ContentType: "application/octet-stream", SizeBytes: response.ContentLength}, nil
}

type trackingArtifactBody struct {
	body      io.ReadCloser
	remaining int64
	once      sync.Once
}

func (b *trackingArtifactBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if int64(len(p)) > b.remaining+1 {
		p = p[:int(b.remaining+1)]
	}
	n, err := b.body.Read(p)
	if int64(n) > b.remaining {
		return 0, errors.New("Kling tracking artifact exceeds its size limit")
	}
	b.remaining -= int64(n)
	if err != nil && err != io.EOF {
		return n, errors.New("Kling tracking artifact could not be read")
	}
	return n, err
}
func (b *trackingArtifactBody) Close() error {
	var err error
	b.once.Do(func() {
		if b.body.Close() != nil {
			err = errors.New("Kling tracking artifact could not be closed")
		}
	})
	return err
}

var _ core.ArtifactOpener = (*Adapter)(nil)
