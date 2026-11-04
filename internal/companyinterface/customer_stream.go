package companyinterface

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// StreamCustomer is reserved media composition. External storage handoffs remain
// explicitly unavailable until the browser/SDK consumer can omit Gateway secrets.
func (c *Coordinator) StreamCustomer(ctx context.Context, prepared CustomerContext, method, path, query string, headers http.Header, keyPresent bool, key string, w http.ResponseWriter) error {
	fence := MediaFence(func(ctx context.Context) error { return c.checkCustomer(ctx, prepared) })
	if e := fence(ctx); e != nil {
		return e
	}
	stream, stop := WatchMedia(ctx, fence)
	defer stop()
	headerDeadline := time.AfterFunc(CustomerHeaderTimeout(method, path), stop)
	response, e := c.central.openCustomerMedia(stream, prepared, method, path, query, headers, keyPresent, key)
	headerDeadline.Stop()
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if e = fence(stream); e != nil {
		return e
	}
	// Never expose an external Location to a client which may retain its original
	// bearer when following redirects. No implicit proxy/relay fallback is allowed.
	if response.StatusCode >= 300 && response.StatusCode < 400 && response.StatusCode != 304 {
		return ErrMediaHandoff
	}
	// Failed protocol authentication precedes media framing. Consume only a small,
	// time-bounded error envelope, then repeat the local epoch fence. Public error
	// messages are reconstructed from fixed protocol codes, never reflected text.
	if response.StatusCode >= 400 && response.Header.Get(MediaCompleteHeader) == "" {
		if _, framed := response.Trailer[http.CanonicalHeaderKey(MediaCompleteHeader)]; !framed {
			failure := readMediaProtocolError(stream, response, method)
			if e := fence(stream); e != nil {
				return e
			}
			return failure
		}
	}
	if response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
		return ErrUnavailable
	}
	if len(response.Header.Values(MediaLengthHeader)) > 1 || len(response.Header.Values(MediaCompleteHeader)) > 1 {
		return ErrUnavailable
	}
	length, e := mediaLength(response.Header.Get(MediaLengthHeader))
	if e != nil {
		return e
	}
	h, e := MediaHeaders(response.Header)
	if e != nil {
		return e
	}
	h.Del("Content-Length")
	if length >= 0 {
		h.Set("Content-Length", strconv.FormatInt(length, 10))
	}
	bodyless := MediaBodyless(method, response.StatusCode)
	// A body-bearing internal stream must be trailer-framed. An ordinary
	// Content-Length cannot be substituted for the adapter-owned length marker.
	if !bodyless && (response.ContentLength >= 0 || response.Header.Get("Content-Length") != "") {
		return ErrUnavailable
	}
	if bodyless && response.Header.Get("Content-Length") != "" && response.Header.Get("Content-Length") != response.Header.Get(MediaLengthHeader) {
		return ErrUnavailable
	}
	if bodyless {
		if response.Header.Get(MediaCompleteHeader) != "complete" {
			return ErrUnavailable
		}
		if e = fence(stream); e != nil {
			return e
		}
		for k, v := range h {
			w.Header()[k] = v
		}
		w.WriteHeader(response.StatusCode)
		return nil
	}
	if response.Header.Get(MediaCompleteHeader) != "" || len(response.Trailer.Values(MediaCompleteHeader)) != 0 {
		// net/http announces trailers as nil-valued keys before EOF. A nonempty
		// header/trailer at this point cannot prove that a body completed.
		return ErrUnavailable
	}
	if _, ok := response.Trailer[http.CanonicalHeaderKey(MediaCompleteHeader)]; !ok {
		return ErrUnavailable
	}
	sink := NewMediaWriter(stream, w, method, false, fence, stop)
	defer sink.Close()
	for k, v := range h {
		sink.Header()[k] = v
	}
	sink.WriteHeader(response.StatusCode)
	failed := func(err error) error {
		if sink.Started() {
			panic(http.ErrAbortHandler)
		}
		return err
	}
	buffer := make([]byte, MediaChunkSize)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if _, e = sink.Write(buffer[:n]); e != nil {
				return failed(e)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return failed(ErrUnavailable)
		}
	}
	if len(response.Trailer.Values(MediaCompleteHeader)) != 1 || response.Trailer.Get(MediaCompleteHeader) != "complete" {
		return failed(ErrUnavailable)
	}
	if e = fence(stream); e != nil {
		return failed(e)
	}
	if e = sink.Finish(); e != nil {
		return failed(e)
	}
	return nil
}
func (c *Client) openCustomerMedia(ctx context.Context, prepared CustomerContext, method, path, query string, headers http.Header, keyPresent bool, key string) (*http.Response, error) {
	binding := prepared.snapshot.Installation
	if binding.CentralOrigin != c.origin || !ValidSecret(binding.Credential) || !ValidID(binding.Company) {
		return nil, ErrDenied
	}
	browser := prepared.active != nil
	if keyPresent && (len(key) < 16 || len(key) > 512) || !browser && !keyPresent {
		return nil, ErrDenied
	}
	contract := Classify(method, path)
	if !contract.Stream {
		return nil, ErrDenied
	}
	shape := Principal{Grant: Grant{Company: binding.Company}, Kind: CentralKey, Scopes: []string{contract.Scope}}
	if browser && (!keyPresent || contract.BrowserManagement) {
		shape.Kind = CentralSession
	}
	contract, e := RequestContract(method, path, query, nil, shape)
	if e != nil {
		return nil, e
	}
	forwarded, e := ForwardHeaders(contract, headers)
	if e != nil {
		return nil, e
	}
	prefix := CustomerSDKPrefix
	if browser {
		prefix = CustomerBrowserPrefix
	}
	destination := c.origin + prefix + strings.TrimPrefix(path, "/")
	if query != "" {
		destination += "?" + query
	}
	req, e := newCustomerRequest(ctx, method, destination, nil)
	if e != nil {
		return nil, ErrDenied
	}
	req.Header = forwarded
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set(InstallationHeader, binding.Credential)
	req.Header.Set(LocalOriginHeader, binding.LocalOrigin)
	req.Header.Set("User-Agent", "Open-Media-Gateway company media v1")
	if browser {
		t := prepared.active.Tokens
		req.Header.Set(SessionHeader, t.AccessToken)
		req.Header.Set(LocalSessionHeader, hex.EncodeToString(t.LocalSessionHash[:]))
	}
	if keyPresent {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := *c.http
	client.Timeout = CustomerTimeout(method, path)
	// The fixed public transport remains unchanged, proxy-disabled and checked-IP.
	// Explicit identity encoding prevents net/http's transparent decompression.
	response, e := client.Do(req)
	if e != nil {
		return nil, ErrUnavailable
	}
	return response, nil
}

func readMediaProtocolError(ctx context.Context, response *http.Response, method string) error {
	check, cancel := context.WithTimeout(ctx, MediaAuthorityTimeout)
	defer cancel()
	closeRead := context.AfterFunc(check, func() { response.Body.Close() })
	defer closeRead()
	raw, e := io.ReadAll(io.LimitReader(response.Body, MaxProtocolBody+1))
	if e != nil || check.Err() != nil || len(raw) > MaxProtocolBody {
		return ErrUnavailable
	}
	code := ""
	if method != http.MethodHead {
		if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
			return ErrUnavailable
		}
		var problem struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &problem) != nil {
			return ErrUnavailable
		}
		code = problem.Error.Code
	}
	switch response.StatusCode {
	case http.StatusForbidden:
		if method == http.MethodHead || code == "company_interface_denied" {
			return ErrDenied
		}
	case http.StatusConflict:
		if code == "company_interface_update_required" {
			return ErrProtocol
		}
		if method == http.MethodHead || code == "company_interface_conflict" {
			return ErrConflict
		}
	case http.StatusPreconditionRequired:
		if method == http.MethodHead || code == "company_interface_pending" {
			return ErrPending
		}
	case http.StatusTooManyRequests:
		if (method == http.MethodHead || code == "company_interface_rate_limit") && response.Header.Get("Retry-After") == "60" && len(response.Header.Values("Retry-After")) == 1 {
			return ErrRateLimited
		}
	}
	return ErrUnavailable
}
