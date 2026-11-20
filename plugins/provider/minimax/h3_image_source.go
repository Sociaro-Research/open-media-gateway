package minimax

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
)

type h3ImageSource struct{ URL, Format, Size string }
type h3ImageReference struct {
	Source h3ImageSource
	Token  string
}

func (s h3ImageSource) digest() string {
	b, _ := json.Marshal(s)
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

func (a *h3Images) sourceReference(ctx context.Context, s h3ImageSource) string {
	b, _ := json.Marshal(h3ImageReference{s, a.seal(ctx, handle{ID: "0", Kind: "h3-image-source", InputHash: s.digest()})})
	return string(b)
}

type h3ImageReader struct {
	io.Reader
	io.Closer
}

// The original native URL and declared dimensions live inside the private
// receipt. Only a bound source can be opened, with a fresh unauthenticated GET.
func (a *h3Images) OpenArtifact(ctx context.Context, ref string) (core.ArtifactSource, error) {
	bad := errors.New("MiniMax image source could not be verified")
	var binding h3ImageReference
	if len(ref) > 65536 || json.Unmarshal([]byte(ref), &binding) != nil {
		return core.ArtifactSource{}, bad
	}
	s := binding.Source
	h, err := a.open(ctx, binding.Token)
	if err != nil || h.Kind != "h3-image-source" || h.InputHash != s.digest() || !publicURL(s.URL) || !slices.Contains([]string{"png", "jpeg", "webp"}, s.Format) {
		return core.ArtifactSource{}, bad
	}
	w, height, ok := h3Pair(s.Size, h3Dimensions)
	if !ok || w*height < 655360 || w*height > 20000000 {
		return core.ArtifactSource{}, bad
	}
	client := httpclient.PublicMediaClient(a.client)
	if client.Timeout <= 0 {
		client.Timeout = time.Minute
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return core.ArtifactSource{}, bad
	}
	resp, err := client.Do(req)
	if err != nil {
		return core.ArtifactSource{}, bad
	}
	if resp.StatusCode != 200 || resp.ContentLength > core.MaxAcceptedReceiptInlineBytes {
		resp.Body.Close()
		return core.ArtifactSource{}, bad
	}
	body := http.MaxBytesReader(nil, resp.Body, core.MaxAcceptedReceiptInlineBytes)
	var prefix bytes.Buffer
	reader := io.TeeReader(io.LimitReader(body, 1<<20), &prefix)
	var x, y int
	if s.Format == "webp" {
		var header [30]byte
		if _, err = io.ReadFull(reader, header[:]); err == nil {
			var info mediafile.Info
			info, err = mediafile.Inspect(header[:], "image/webp")
			x, y = info.Width, info.Height
		}
	} else {
		var config image.Config
		var format string
		config, format, err = image.DecodeConfig(reader)
		if format != s.Format {
			err = bad
		}
		x, y = config.Width, config.Height
	}
	if err != nil || int64(x) != w || int64(y) != height {
		body.Close()
		return core.ArtifactSource{}, bad
	}
	return core.ArtifactSource{Reader: h3ImageReader{io.MultiReader(&prefix, body), body}, ContentType: "image/" + s.Format, SizeBytes: resp.ContentLength}, nil
}
