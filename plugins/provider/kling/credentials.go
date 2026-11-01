package kling

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

const credentialCacheTTL = 5 * time.Minute

const packageDetailsWarning = "Resource-package details were absent or malformed; package availability and balances remain unverified."

// All fresh account queries in this process share a one-per-second gate,
// including duplicate connections with the same key. Other processes and
// external clients can still encounter the provider's account-wide rate limit.
var accountQueryGate = make(chan struct{}, 1)
var nextAccountQuery time.Time // protected by accountQueryGate

func (a *Adapter) CredentialStatus() providerkit.CredentialCheck {
	a.checkMu.Lock()
	defer a.checkMu.Unlock()
	if time.Since(a.check.CheckedAt) >= credentialCacheTTL {
		return providerkit.CredentialCheck{}
	}
	return a.check
}

func (a *Adapter) CheckCredentials(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case a.checkGate <- struct{}{}:
		defer func() { <-a.checkGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	previous := a.CredentialStatus()
	if !previous.CheckedAt.IsZero() && (previous.Err == nil || time.Since(previous.CheckedAt) < time.Second) {
		return previous.Err
	}
	started := time.Now()
	select {
	case accountQueryGate <- struct{}{}:
		defer func() { <-accountQueryGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if delay := time.Until(nextAccountQuery); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	now := time.Now().UTC()
	nextAccountQuery = now.Add(time.Second)
	query := url.Values{"start_time": {strconv.FormatInt(now.Add(-24*time.Hour).UnixMilli(), 10)}, "end_time": {strconv.FormatInt(now.UnixMilli(), 10)}}
	raw, err := a.do(providerkit.WithCredentialCheck(ctx), http.MethodGet, "/account/costs?"+query.Encode(), nil)
	warning := ""
	if err == nil {
		// Inspect only the envelope/schema. Never retain package names, balances,
		// quantities or account IDs as connection metadata or log output.
		var data struct {
			Code     *int            `json:"code"`
			Packages json.RawMessage `json:"resource_pack_subscribe_infos"`
		}
		if json.Unmarshal(raw, &data) != nil || data.Code == nil {
			err = errors.New("Kling account query returned a malformed response; authentication is unverified")
		} else if *data.Code != 0 {
			err = serviceError(*data.Code)
		} else {
			var packages []json.RawMessage
			if len(data.Packages) == 0 || string(data.Packages) == "null" || json.Unmarshal(data.Packages, &packages) != nil {
				// Package data is not an authentication factor: both service
				// codes already report success. Keep its absence visible without
				// inferring a balance, subscription or model entitlement.
				warning = packageDetailsWarning
			}
		}
	}
	a.checkMu.Lock()
	a.check = providerkit.CredentialCheck{CheckedAt: time.Now().UTC(), LatencyMS: time.Since(started).Milliseconds(), Err: err, Warning: warning}
	a.checkMu.Unlock()
	return err
}
