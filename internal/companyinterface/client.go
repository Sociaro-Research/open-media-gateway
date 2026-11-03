package companyinterface

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
)

const ProtocolBase = "/auth/company-interface/v1"
const MaxProtocolBody = 32 << 10
const ProtocolTimeout = 15 * time.Second

var ErrUnavailable = errors.New("central company authorization is unavailable; the pending operation is retained")
var ErrRateLimited = errors.New("central company authorization rate limit reached")

// Client has one trusted configured destination and fixed typed operations. It
// never accepts a browser URL, cookie, identity header or generic forwarding path.
// NewPublic validates all resolved addresses at connection time and pins dialing;
// no environment proxy, private address exception or redirect is permitted.
type Client struct {
	origin string
	http   *http.Client
}

func NewClient(origin string) (*Client, error) {
	normalized, e := installation.NormalizeOrigin(origin)
	if e != nil || normalized != origin || !strings.HasPrefix(origin, "https://") {
		return nil, ErrDenied
	}
	h := httpclient.NewPublic(ProtocolTimeout)
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{origin: origin, http: h}, nil
}
func (c *Client) Origin() string { return c.origin }
func (c *Client) request(ctx context.Context, operation string, in, out any) error {
	switch operation {
	case "capabilities", "start", "redeem", "refresh", "cancel", "reconcile", "recover-installation", "logout", "revoke-issued-family":
	default:
		return ErrDenied
	}
	var raw []byte
	var e error
	method := http.MethodPost
	if operation == "capabilities" {
		method = http.MethodGet
	} else {
		raw, e = json.Marshal(in)
		if e != nil || len(raw) > MaxProtocolBody {
			return ErrDenied
		}
	}
	ctx, cancel := context.WithTimeout(ctx, ProtocolTimeout)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, method, c.origin+ProtocolBase+"/"+operation, bytes.NewReader(raw))
	if e != nil {
		return ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Open-Media-Gateway company authorization v1")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	response, e := c.http.Do(req)
	if e != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, MaxProtocolBody+1))
	if e != nil || len(body) > MaxProtocolBody {
		return ErrUnavailable
	}
	// Status/error codes are bounded public protocol values. Never reflect an
	// arbitrary upstream body, URL, token or network exception into browser logs.
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusNotImplemented {
			return ErrProtocol
		}
		var problem struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &problem) == nil {
			switch problem.Error.Code {
			case "company_interface_update_required":
				return ErrProtocol
			case "company_interface_conflict":
				return ErrConflict
			case "company_interface_pending":
				return ErrPending
			case "company_interface_denied":
				return ErrDenied
			case "company_interface_rate_limit":
				return ErrRateLimited
			}
		}
		return ErrUnavailable
	}
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "application/json") {
		return ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(out); e != nil {
		return ErrUnavailable
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ErrUnavailable
	}
	return nil
}
func (c *Client) Capabilities(ctx context.Context) (Protocol, error) {
	var p Protocol
	e := c.request(ctx, "capabilities", nil, &p)
	return p, e
}

// RequireFullSurface must succeed before a local mode/pending-flow transition.
// An old or incomplete central instance produces a meaningful update-required
// result without changing local connection state or falling back to local data.
func (c *Client) RequireFullSurface(ctx context.Context) error {
	p, e := c.Capabilities(ctx)
	if e != nil {
		return e
	}
	return Negotiate(p)
}
func (c *Client) Start(ctx context.Context, in Start) (Challenge, error) {
	var v Challenge
	e := c.request(ctx, "start", in, &v)
	if e == nil && (v.ID != in.AttemptID || v.Code == "" || v.State != "pending") {
		e = ErrConflict
	}
	return v, e
}
func (c *Client) Redeem(ctx context.Context, in Redemption) (Tokens, error) {
	var v Tokens
	e := c.request(ctx, "redeem", in, &v)
	if e == nil && (v.AttemptID != in.AttemptID || v.CentralOrigin != c.origin || v.LocalOrigin != in.LocalOrigin || v.InstallationHash != InstallationDigest(in.InstallationProof) || v.LocalSessionHash != Digest(in.LocalSession) || v.ConnectionRevision != in.ConnectionRevision) {
		e = ErrConflict
	}
	return v, e
}
func (c *Client) Refresh(ctx context.Context, in Refresh) (Tokens, error) {
	var v Tokens
	e := c.request(ctx, "refresh", in, &v)
	if e == nil && (v.SessionID != in.SessionID || v.Version != in.Version+1 || v.LocalOrigin != in.LocalOrigin || v.CentralOrigin != c.origin || v.LocalSessionHash != Digest(in.LocalSession)) {
		e = ErrConflict
	}
	return v, e
}
func (c *Client) Cancel(ctx context.Context, in Redemption) error {
	var v struct {
		Cancelled bool `json:"cancelled"`
	}
	e := c.request(ctx, "cancel", in, &v)
	if e == nil && !v.Cancelled {
		return ErrConflict
	}
	return e
}
func (c *Client) Reconcile(ctx context.Context, in Redemption) (Reconciliation, error) {
	var v Reconciliation
	e := c.request(ctx, "reconcile", in, &v)
	if e == nil && (v.Binding.ID != in.AttemptID || v.Binding.CentralOrigin != c.origin || v.Binding.LocalOrigin != in.LocalOrigin || v.Binding.InstallationHash != InstallationDigest(in.InstallationProof) || v.Binding.ProofHash != Digest(in.Proof) || v.Binding.LocalSessionHash != Digest(in.LocalSession) || v.Binding.ConnectionRevision != in.ConnectionRevision || v.Fingerprint != Fingerprint(v.Binding)) {
		e = ErrConflict
	}
	return v, e
}
func (c *Client) RecoverInstallation(ctx context.Context, in Redemption) (LocalInstallation, error) {
	var v LocalInstallation
	e := c.request(ctx, "recover-installation", in, &v)
	if e == nil && (v.CentralOrigin != c.origin || v.LocalOrigin != in.LocalOrigin || v.InstallationHash != InstallationDigest(in.InstallationProof) || !ValidSecret(v.Credential)) {
		e = ErrConflict
	}
	return v, e
}

// Logout only revokes the separately verified delegated browser family.
func (c *Client) Logout(ctx context.Context, in Credentials) error {
	var v struct {
		LoggedOut bool `json:"logged_out"`
	}
	e := c.request(ctx, "logout", in, &v)
	if e == nil && !v.LoggedOut {
		return ErrConflict
	}
	return e
}

// RevokeIssuedFamily consumes only the original attempt proof and returns no
// authority. It cannot name a different family or permanent grant to revoke.
func (c *Client) RevokeIssuedFamily(ctx context.Context, in Redemption) error {
	var v struct {
		Revoked bool `json:"revoked"`
	}
	e := c.request(ctx, "revoke-issued-family", in, &v)
	if e == nil && !v.Revoked {
		return ErrConflict
	}
	return e
}
