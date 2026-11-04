package companyinterface

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"
)

const MaxCustomerBody = 8192
const MaxCustomerResponse = 8 << 20

// InstallationSnapshot and CustomerContext are server-private. No HTTP request
// accepts either value from a browser, and neither is a user credential.
type InstallationSnapshot struct {
	Installation LocalInstallation
	Revision     int64
}
type CustomerStore interface {
	ActiveStore
	LocalInstallationSnapshot(context.Context) (InstallationSnapshot, error)
	CheckLocalInstallation(context.Context, InstallationSnapshot) error
}
type CustomerContext struct {
	snapshot InstallationSnapshot
	active   *ActiveContext
}
type CustomerResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

func (c *Coordinator) PrepareCustomer(ctx context.Context, local string, browser bool) (CustomerContext, error) {
	var out CustomerContext
	// Production remains unavailable until both complete surface contracts exist.
	// This precedes refresh or any other mutation; fixtures negotiate explicitly.
	if e := c.ready(ctx); e != nil {
		return out, e
	}
	store, ok := c.store.(CustomerStore)
	if !ok {
		return out, ErrUnavailable
	}
	if browser {
		active, e := store.LocalActive(ctx, local)
		if e != nil {
			return out, e
		}
		if active.Tokens.LocalOrigin != c.own || active.Tokens.CentralOrigin != c.central.Origin() {
			return out, ErrConflict
		}
		if !active.Tokens.AccessExpiresAt.After(time.Now().Add(30 * time.Second)) {
			active, e = c.RefreshSession(ctx, local, active.Epoch, active.Tokens.Version)
			if e != nil {
				return out, e
			}
		}
		out.active = &active
		t := active.Tokens
		out.snapshot = InstallationSnapshot{Installation: LocalInstallation{GrantID: t.GrantID, Company: t.Company, CentralOrigin: t.CentralOrigin, LocalOrigin: t.LocalOrigin, Credential: t.InstallationCredential, InstallationHash: t.InstallationHash, GrantEpoch: t.GrantEpoch}, Revision: active.Revision}
	} else {
		var e error
		out.snapshot, e = store.LocalInstallationSnapshot(ctx)
		if e != nil {
			return out, e
		}
	}
	if out.snapshot.Installation.LocalOrigin != c.own || out.snapshot.Installation.CentralOrigin != c.central.Origin() {
		return CustomerContext{}, ErrConflict
	}
	return out, c.checkCustomer(ctx, out)
}
func (c *Coordinator) checkCustomer(ctx context.Context, in CustomerContext) error {
	store, ok := c.store.(CustomerStore)
	if !ok {
		return ErrUnavailable
	}
	if in.active != nil {
		a := in.active
		if e := store.CheckLocalEpoch(ctx, a.LocalSession, a.Epoch, a.Revision, a.Tokens.Version, a.Tokens.SessionID, a.Tokens.Actor); e != nil {
			return e
		}
	}
	return store.CheckLocalInstallation(ctx, in.snapshot)
}

func (c *Coordinator) Customer(ctx context.Context, prepared CustomerContext, method, path, query string, body []byte, headers http.Header, keyPresent bool, key string) (CustomerResponse, error) {
	var empty CustomerResponse
	if e := c.checkCustomer(ctx, prepared); e != nil {
		return empty, e
	}
	response, e := c.central.customer(ctx, prepared, method, path, query, body, headers, keyPresent, key)
	// Fence after the complete remote body even when central returns an error.
	// No uncertain mutation is resent and there is no local-data fallback.
	if fence := c.checkCustomer(ctx, prepared); fence != nil {
		return empty, fence
	}
	if e != nil {
		return empty, e
	}
	return response, nil
}
func (c *Client) customer(ctx context.Context, prepared CustomerContext, method, path, query string, body []byte, headers http.Header, keyPresent bool, key string) (CustomerResponse, error) {
	var empty CustomerResponse
	binding := prepared.snapshot.Installation
	if binding.CentralOrigin != c.origin || !ValidSecret(binding.Credential) || !ValidID(binding.Company) {
		return empty, ErrDenied
	}
	browser := prepared.active != nil
	if keyPresent && (len(key) < 16 || len(key) > 512) || !browser && !keyPresent {
		return empty, ErrDenied
	}
	contract := Classify(method, path)
	// This local object checks shape/tenant only. It grants no actor authority:
	// central Gate validates the original key and current user/recipient policy.
	shape := Principal{Grant: Grant{Company: binding.Company}, Kind: CentralKey, Scopes: []string{contract.Scope}}
	if browser && (!keyPresent || contract.BrowserManagement) {
		shape.Kind = CentralSession
	}
	contract, e := RequestContract(method, path, query, body, shape)
	if e != nil {
		return empty, e
	}
	body, e = restoreMediaRequest(contract, body, binding.Company, c.origin, binding.LocalOrigin)
	if e != nil {
		return empty, e
	}
	forwarded, e := ForwardHeaders(contract, headers)
	if e != nil {
		return empty, e
	}
	prefix := CustomerSDKPrefix
	if browser {
		prefix = CustomerBrowserPrefix
	}
	destination := c.origin + prefix + strings.TrimPrefix(path, "/")
	if query != "" {
		destination += "?" + query
	}
	ctx, cancel := context.WithTimeout(ctx, CustomerTimeout(method, path))
	defer cancel()
	req, e := newCustomerRequest(ctx, method, destination, body)
	if e != nil {
		return empty, ErrDenied
	}
	req.Header = forwarded
	req.Header.Set(InstallationHeader, binding.Credential)
	req.Header.Set(LocalOriginHeader, binding.LocalOrigin)
	req.Header.Set("User-Agent", "Open-Media-Gateway company customer v1")
	if browser {
		t := prepared.active.Tokens
		req.Header.Set(SessionHeader, t.AccessToken)
		req.Header.Set(LocalSessionHeader, hex.EncodeToString(t.LocalSessionHash[:]))
	}
	if keyPresent {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	// Copy only the client configuration; reuse the fixed public transport and
	// redirect rejection. Its response-header timeout is not tied to the short
	// protocol deadline. Never mutate a client shared by concurrent requests.
	client := *c.http
	client.Timeout = CustomerTimeout(method, path)
	response, e := client.Do(req)
	if e != nil {
		return empty, ErrUnavailable
	}
	defer response.Body.Close()
	// Redirects remain terminal; never send credentials to response destinations.
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return empty, ErrUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, MaxCustomerResponse+1))
	if e != nil || len(raw) > MaxCustomerResponse {
		return empty, ErrUnavailable
	}
	raw, location, e := generationStatusResponse(contract, response.StatusCode, method, path, binding.Company, response.Header, raw)
	if e != nil {
		return empty, e
	}
	raw, e = mediaJSONResponse(contract, response.StatusCode, method, path, binding.Company, c.origin, raw)
	if e != nil {
		return empty, e
	}
	raw, e = paymentJSONResponse(contract, response.StatusCode, method, path, binding.Company, raw)
	if e != nil {
		return empty, e
	}
	if e = paymentBinaryResponse(contract, response.StatusCode, method, response.Header, raw); e != nil {
		return empty, e
	}
	out := CustomerResponse{Status: response.StatusCode, Header: http.Header{}, Body: raw}
	if location != "" {
		out.Header.Set("Location", location)
	}
	for _, name := range []string{"Content-Type", "Content-Disposition", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Retry-After"} {
		// Original entity validators describe central JSON bytes, which differ
		// after the typed status URL mapping (including the paired HEAD view).
		if name == "ETag" && contract.ResponseURLs != "none" {
			continue
		}
		values := response.Header.Values(name)
		if len(values) > 1 {
			return empty, ErrUnavailable
		}
		if len(values) == 1 {
			if len(values[0]) > 4096 || strings.ContainsAny(values[0], "\r\n\x00") {
				return empty, ErrUnavailable
			}
			out.Header.Set(name, values[0])
		}
	}
	return out, nil
}

// A stable idempotency key supports an explicit caller retry; it must not make
// net/http automatically resend an uncertain mutation. A non-rewindable body is
// retained even for zero bytes: nil/http.NoBody plus an idempotency header would
// also make an empty request replayable. Reads keep ordinary transport behavior.
func newCustomerRequest(ctx context.Context, method, destination string, body []byte) (*http.Request, error) {
	if method == http.MethodGet || method == http.MethodHead {
		return http.NewRequestWithContext(ctx, method, destination, bytes.NewReader(body))
	}
	request, e := http.NewRequestWithContext(ctx, method, destination, io.NopCloser(bytes.NewReader(body)))
	if e != nil {
		return nil, e
	}
	request.ContentLength = int64(len(body))
	return request, nil
}
