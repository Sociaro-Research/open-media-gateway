package minimax

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
)

var h3ImageRoutes = httpclient.SinglePOSTRoutes{
	Origin: DefaultBaseURL, Paths: []string{"/v1/images/generations", "/v1/images/edits"}, MinimumWait: 10 * time.Minute,
}

// H3 image returns native JSON without the legacy base_resp envelope. The
// catalog stays closed until the dedicated serializers and accounting are ready.
func (a *Adapter) readH3ImageResponse(req *http.Request, path string) ([]byte, error) {
	resp, err := h3ImageRoutes.Do(a.client, req, path, a.connectionTimeout)
	if errors.Is(err, httpclient.ErrSinglePOSTConfiguration) {
		return nil, httpclient.ErrSinglePOSTConfiguration
	}
	if err != nil {
		return nil, core.ErrProviderSubmissionUncertain
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestEntityTooLarge, http.StatusTooManyRequests:
		return nil, &APIError{Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != 402 && resp.StatusCode != 422 && resp.StatusCode != 500 {
		return nil, core.ErrProviderSubmissionUncertain
	}
	limit := core.MaxAcceptedReceiptBytes
	if resp.StatusCode != http.StatusOK {
		limit = 256 << 10
		typ, _, parseErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if parseErr != nil || typ != "application/json" {
			return nil, core.ErrProviderSubmissionUncertain
		}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil || len(data) > limit || !json.Valid(data) {
		return nil, core.ErrProviderSubmissionUncertain
	}
	if resp.StatusCode != http.StatusOK {
		return data, &APIError{Status: resp.StatusCode}
	}
	return data, nil
}
