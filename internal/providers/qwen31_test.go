package providers

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

func TestQwen31ManagedReceiptUsesPublicMediaTransportAfterKeyRotation(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		t.Run(strconv.FormatBool(redirect), func(t *testing.T) {
			m, _, ctx := fixtureConnection(t)
			posts, downloads := 0, 0
			redirects, policyCalls := 0, 0
			m.client.CheckRedirect = func(r *http.Request, _ []*http.Request) error {
				policyCalls++
				if r.URL.Scheme != "https" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Private") != "" || r.Header.Get("Cookie") != "" {
					t.Fatal("original media redirect policy saw credentials or unsafe URL")
				}
				return nil
			}
			m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := `{"output":{"total":0,"models":[]}}`
				if r.URL.Host == "audio.example.test" || r.URL.Host == "final-media.example.test" {
					if r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Private") != "" || r.Header.Get("Cookie") != "" {
						t.Fatal("API credentials reached the media origin")
					}
					if redirect && r.URL.Host == "audio.example.test" {
						redirects++
						return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://final-media.example.test/result.wav"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
					}
					downloads++
					wav := testmedia.WAV()
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"audio/wav"}}, ContentLength: int64(len(wav)), Body: io.NopCloser(strings.NewReader(string(wav)))}, nil
				}
				if r.Method == "POST" {
					posts++
					if !strings.HasSuffix(r.URL.Path, "SpeechSynthesizer") || r.Header.Get("Authorization") != "Bearer synthetic-qwen-key" || r.Header.Get("X-Private") != "api-only" {
						t.Fatal("lost configured native API transport")
					}
					body = `{"output":{"finish_reason":"stop","audio":{"id":"accepted-qwen","url":"https://audio.example.test/result.wav","expires_at":` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}},"usage":{"duration":1}}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			c, err := m.Create(ctx, CreateConnection{APIType: "alibaba", Name: "Offline Qwen transport"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Update(ctx, c.ID, ProviderUpdate{APIKey: pointer("synthetic-qwen-key"), BaseURL: pointer("https://fixture.ap-southeast-1.maas.aliyuncs.com/api/v1"), Enabled: pointer(true), Connection: &providerkit.Connection{Headers: []providerkit.Header{{Name: "X-Private", Value: pointer("api-only")}}}}); err != nil {
				t.Fatal(err)
			}
			p, err := m.Provider(ctx, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			r := core.MediaRequest{Model: "qwen-audio-3.1-tts-next", Prompt: "Synthetic rain", Options: core.MediaOptions{Format: "wav"}}
			_, revision, err := p.(core.CostQuoter).Quote(ctx, core.MediaModel{ID: r.Model}, r)
			if err != nil || revision == "" {
				t.Fatal(revision, err)
			}
			accepted, err := p.Generate(core.WithProviderRevision(ctx, revision), r)
			if err != nil || len(accepted.Artifacts) != 1 || downloads != 0 {
				t.Fatal("result was not retained before download", accepted, err)
			}
			if _, err = m.Update(ctx, c.ID, ProviderUpdate{APIKey: pointer("rotated-synthetic-key")}); err != nil {
				t.Fatal(err)
			}
			source, err := p.(core.ArtifactOpener).OpenArtifact(core.WithProviderRevision(ctx, revision), accepted.Artifacts[0].SourceReference)
			if err != nil {
				t.Fatal("pinned receipt failed to download", err)
			}
			data, err := io.ReadAll(source.Reader)
			_ = source.Reader.Close()
			if err != nil || string(data) != string(testmedia.WAV()) || posts != 1 || downloads != 1 {
				t.Fatal("result corrupted or resubmitted", posts, downloads, err)
			}
			wantRedirects := 0
			if redirect {
				wantRedirects = 1
			}
			if redirects != wantRedirects || policyCalls != wantRedirects {
				t.Fatal("original media redirect policy was not preserved", redirects, policyCalls)
			}
		})
	}
}
