package alibaba

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

const qwen31SourcePrefix = "alibaba-qwen31:"
const qwen31MaxOutputBytes = 128 << 20 // Gateway file envelope, not a vendor limit.

type qwen31Source struct {
	Version            int    `json:"version"`
	URL                string `json:"url"`
	Format             string `json:"format"`
	ExpiresAt          int64  `json:"expires_at"`
	Owner              string `json:"owner"`
	DataOwner          string `json:"data_owner"`
	Revision           string `json:"revision"`
	Connection         string `json:"connection"`
	ConnectionRevision string `json:"connection_revision"`
}

func qwen31SourceScope(ctx context.Context) qwen31Source {
	s := qwen31Source{Owner: identity.ProviderOwnerID(ctx), DataOwner: identity.OwnerID(ctx), Revision: core.ProviderRevision(ctx)}
	if scope, ok := core.ProviderConnectionScopeFromContext(ctx); ok {
		s.Connection, s.ConnectionRevision = scope.ID, scope.Revision
	}
	return s
}

func (a *Adapter) qwen31SourceMAC(raw []byte) []byte {
	mac := hmac.New(sha256.New, []byte(a.apiKey))
	_, _ = mac.Write([]byte("alibaba-qwen31-source-v1\x00" + a.baseURL + "\x00"))
	_, _ = mac.Write(raw)
	return mac.Sum(nil)
}

func (a *Adapter) sealQwen31Result(ctx context.Context, result core.ProviderTask, expires int64) (core.ProviderTask, error) {
	if a.apiKey == "" || len(result.Artifacts) != 1 {
		return core.ProviderTask{}, failurePolicy.InvalidConnection()
	}
	result.Artifacts = append([]core.ProviderArtifact(nil), result.Artifacts...)
	source := qwen31SourceScope(ctx)
	source.Version, source.URL, source.Format, source.ExpiresAt = 1, result.ResultURL, result.Artifacts[0].Format, expires
	if source.Owner == "" || source.DataOwner == "" || len(source.Owner) > 4096 || len(source.DataOwner) > 4096 || len(source.Revision) > 4096 || len(source.Connection) > 4096 || len(source.ConnectionRevision) > 4096 {
		return core.ProviderTask{}, failurePolicy.InvalidLocalRequest()
	}
	raw, err := json.Marshal(source)
	if err != nil || len(raw) > 32768 {
		return core.ProviderTask{}, failurePolicy.InvalidLocalRequest()
	}
	result.Artifacts[0].SourceReference = qwen31SourcePrefix + base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(a.qwen31SourceMAC(raw))
	result.Artifacts[0].URL, result.ResultURL = "", ""
	return result, nil
}

// OpenArtifact is only reached after the worker durably saves the private
// accepted result. Scope/MAC/expiry checks precede every outbound request.
func (a *Adapter) OpenArtifact(ctx context.Context, handle string) (core.ArtifactSource, error) {
	bad := func() (core.ArtifactSource, error) {
		return core.ArtifactSource{}, errors.New("Alibaba audio source is unavailable; check the original generation and connection before submitting again")
	}
	if a.apiKey == "" || a.client == nil || len(handle) > 48000 || !strings.HasPrefix(handle, qwen31SourcePrefix) {
		return bad()
	}
	payload, signature, ok := strings.Cut(strings.TrimPrefix(handle, qwen31SourcePrefix), ".")
	if !ok {
		return bad()
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || len(raw) > 32768 {
		return bad()
	}
	mac, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !hmac.Equal(mac, a.qwen31SourceMAC(raw)) {
		return bad()
	}
	var source qwen31Source
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&source) != nil || decoder.Decode(new(any)) != io.EOF {
		return bad()
	}
	scope := qwen31SourceScope(ctx)
	if source.Version != 1 || source.Owner != scope.Owner || source.DataOwner != scope.DataOwner || source.Revision != scope.Revision || source.Connection != scope.Connection || source.ConnectionRevision != scope.ConnectionRevision || source.ExpiresAt <= time.Now().Unix() || !qwen31URL(source.URL) || source.Format != "wav" && source.Format != "mp3" {
		return bad()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return bad()
	}
	client := *httpclient.PublicMediaClient(a.client)
	priorRedirect := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 || next.Method != http.MethodGet || !qwen31URL(next.URL.String()) {
			return errors.New("unsupported Alibaba audio redirect")
		}
		if priorRedirect != nil {
			if err := priorRedirect(next, via); err != nil {
				return err
			}
		}
		next.Header.Del("Authorization")
		next.Header.Del("Cookie")
		return nil
	}
	response, err := httpclient.Do(&client, request)
	if err != nil {
		if ctx.Err() != nil {
			return core.ArtifactSource{}, ctx.Err()
		}
		return bad()
	}
	typ, _, typeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	validType := source.Format == "wav" && (typ == "audio/wav" || typ == "audio/wave" || typ == "audio/x-wav") || source.Format == "mp3" && typ == "audio/mpeg"
	if response.StatusCode != http.StatusOK || response.ContentLength == 0 || response.ContentLength > qwen31MaxOutputBytes || typeErr != nil || !validType {
		_ = response.Body.Close()
		return bad()
	}
	limited := http.MaxBytesReader(nil, response.Body, qwen31MaxOutputBytes)
	reader := bufio.NewReaderSize(limited, 65536)
	size, err := qwen31AudioContainer(reader, source.Format, response.ContentLength)
	if err != nil {
		_ = limited.Close()
		return bad()
	}
	// ArchiveSource checks the complete stream length after this strict known
	// container check; its generic unknown-binary fallback is not sufficient.
	return core.ArtifactSource{Reader: qwen31AudioReader{Reader: reader, Closer: limited}, ContentType: typ, SizeBytes: size}, nil
}

type qwen31AudioReader struct {
	io.Reader
	io.Closer
}

// Require a known WAV container or a complete initial MPEG Layer III frame.
// Prelude inspection is bounded to 64 KiB; raw PCM and unknown binary are never
// accepted just because the server labels them audio. This is container
// validation, not a claim of full audio decoding or acoustic quality analysis.
func qwen31AudioContainer(reader *bufio.Reader, format string, size int64) (int64, error) {
	bad := errors.New("unsupported Alibaba audio container")
	if format == "wav" {
		header, err := reader.Peek(12)
		if err != nil || string(header[:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
			return 0, bad
		}
		total := int64(binary.LittleEndian.Uint32(header[4:8])) + 8
		if total < 44 || total > qwen31MaxOutputBytes || size >= 0 && size != total {
			return 0, bad
		}
		foundFormat := false
		for offset := int64(12); offset+8 <= total && offset+8 <= 65536; {
			prefix, err := reader.Peek(int(offset + 8))
			if err != nil {
				return 0, bad
			}
			kind := string(prefix[offset : offset+4])
			length := int64(binary.LittleEndian.Uint32(prefix[offset+4 : offset+8]))
			end := offset + 8 + length
			if end+length%2 > total {
				return 0, bad
			}
			if kind == "fmt " {
				if foundFormat || length < 16 || offset+24 > 65536 {
					return 0, bad
				}
				prefix, err = reader.Peek(int(offset + 24))
				if err != nil || binary.LittleEndian.Uint16(prefix[offset+8:]) == 0 || binary.LittleEndian.Uint16(prefix[offset+10:]) == 0 || binary.LittleEndian.Uint32(prefix[offset+12:]) == 0 || binary.LittleEndian.Uint16(prefix[offset+20:]) == 0 {
					return 0, bad
				}
				foundFormat = true
			}
			if kind == "data" {
				if !foundFormat || length == 0 {
					return 0, bad
				}
				return total, nil
			}
			offset = end + length%2
		}
		return 0, bad
	}
	if format != "mp3" {
		return 0, bad
	}
	offset := 0
	prefix, err := reader.Peek(4)
	if err != nil {
		return 0, bad
	}
	if string(prefix[:3]) == "ID3" {
		prefix, err = reader.Peek(10)
		if err != nil || prefix[3] < 2 || prefix[3] > 4 {
			return 0, bad
		}
		for _, b := range prefix[6:10] {
			if b&0x80 != 0 {
				return 0, bad
			}
			offset = offset<<7 | int(b)
		}
		offset += 10
		if prefix[3] == 4 && prefix[5]&0x10 != 0 {
			offset += 10 // ID3v2.4 optional footer.
		}
		if offset+4 > 65536 {
			return 0, bad
		}
	}
	prefix, err = reader.Peek(offset + 4)
	if err != nil {
		return 0, bad
	}
	h := prefix[offset:]
	version, layer, rateIndex, sampleIndex := (h[1]>>3)&3, (h[1]>>1)&3, h[2]>>4, (h[2]>>2)&3
	if h[0] != 0xff || h[1]&0xe0 != 0xe0 || version == 1 || layer != 1 || rateIndex == 0 || rateIndex == 15 || sampleIndex == 3 {
		return 0, bad
	}
	rates := []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	sampleRate, factor := []int{44100, 48000, 32000}[sampleIndex], 144000
	if version != 3 {
		rates = []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
		sampleRate, factor = sampleRate/2, 72000
		if version == 0 {
			sampleRate /= 2
		}
	}
	frameLength := factor*rates[rateIndex]/sampleRate + int((h[2]>>1)&1)
	if offset+frameLength > 65536 || size >= 0 && int64(offset+frameLength) > size {
		return 0, bad
	}
	if _, err := reader.Peek(offset + frameLength); err != nil {
		return 0, bad
	}
	return size, nil
}
