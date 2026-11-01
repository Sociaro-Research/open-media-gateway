package agnes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

// Catalog fallback and ordinary discovery never establish authentication.
// The live read-only endpoint rejected missing and invalid keys on 2026-10-30.
func (a *Adapter) CredentialStatus() providerkit.CredentialCheck {
	a.checkMu.Lock()
	defer a.checkMu.Unlock()
	if time.Since(a.check.CheckedAt) >= 5*time.Minute {
		return providerkit.CredentialCheck{}
	}
	return a.check
}

func (a *Adapter) CheckCredentials(ctx context.Context) error {
	select {
	case a.checkGate <- struct{}{}:
		defer func() { <-a.checkGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	started := time.Now()
	raw, _, err := a.do(providerkit.WithCredentialCheck(ctx), http.MethodGet, "/models", nil, false)
	if err == nil {
		var body struct {
			Data    json.RawMessage `json:"data"`
			Error   json.RawMessage `json:"error"`
			Code    json.RawMessage `json:"code"`
			Success *bool           `json:"success"`
		}
		var models []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &body) != nil || (len(body.Error) > 0 && string(body.Error) != "null") ||
			(len(body.Code) > 0 && string(body.Code) != "0" && string(body.Code) != `"0"`) || (body.Success != nil && !*body.Success) ||
			len(body.Data) == 0 || string(body.Data) == "null" || json.Unmarshal(body.Data, &models) != nil {
			err = errors.New("Agnes authentication query returned an unsuccessful or malformed response; API key remains unverified")
		} else {
			for _, model := range models {
				if strings.TrimSpace(model.ID) == "" {
					err = errors.New("Agnes authentication query returned malformed model metadata; API key remains unverified")
					break
				}
			}
		}
	}
	a.checkMu.Lock()
	defer a.checkMu.Unlock()
	a.check = providerkit.CredentialCheck{CheckedAt: time.Now().UTC(), LatencyMS: time.Since(started).Milliseconds(), Err: err}
	return err
}
