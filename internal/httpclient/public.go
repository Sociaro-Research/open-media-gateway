package httpclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/webhookurl"
)

var ErrOutboundDestination = errors.New("outbound destination must use a permitted HTTP(S) network address")

type publicTransport struct {
	base    *http.Transport
	private map[string]bool
}

func carriesOutboundSecrets(request *http.Request) bool {
	for name := range request.Header {
		switch strings.ToLower(name) {
		case "accept", "accept-encoding", "user-agent", "range", "if-range":
		default:
			return true
		}
	}
	return request.Body != nil && request.Body != http.NoBody
}

func NewPublic(timeout time.Duration, privateHosts ...string) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return newPublic(timeout, privateHosts, net.DefaultResolver.LookupNetIP, dialer.DialContext)
}

func newPublic(timeout time.Duration, hosts []string, lookup func(context.Context, string, string) ([]netip.Addr, error), dial func(context.Context, string, string) (net.Conn, error)) *http.Client {
	client := New(timeout)
	base := client.Transport.(*http.Transport)
	allowed := map[string]bool{}
	for _, host := range hosts {
		allowed[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))] = true
	}
	base.Proxy = nil // An environment proxy would bypass checked-IP dialing.
	base.DialTLSContext = nil
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrOutboundDestination
		}
		var ips []netip.Addr
		if ip, e := netip.ParseAddr(host); e == nil {
			ips = []netip.Addr{ip}
		} else {
			ips, err = lookup(ctx, "ip", host)
			if err != nil {
				return nil, ErrOutboundDestination
			}
		}
		if len(ips) == 0 {
			return nil, ErrOutboundDestination
		}
		for _, ip := range ips {
			if !ip.IsValid() || ip.Zone() != "" || (!webhookurl.PublicIP(ip) && !allowed[strings.ToLower(strings.TrimSuffix(host, "."))]) {
				return nil, ErrOutboundDestination
			}
		}
		for _, ip := range ips {
			conn, e := dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
			if e == nil {
				return conn, nil
			}
			err = e
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, err
	}
	client.Transport = &publicTransport{base: base, private: allowed}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many outbound redirects")
		}
		previous := via[len(via)-1]
		if previous.URL.Scheme == "https" && request.URL.Scheme != "https" {
			return ErrOutboundDestination
		}
		if !strings.EqualFold(previous.URL.Host, request.URL.Host) || previous.URL.Scheme != request.URL.Scheme {
			if carriesOutboundSecrets(previous) {
				return errors.New("credential-bearing outbound request cannot redirect to another origin")
			}
		}
		return nil
	}
	return client
}

func (t *publicTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	u := request.URL
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, ErrOutboundDestination
	}
	if !t.private[strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))] && webhookurl.Validate(u.String()) != nil {
		return nil, ErrOutboundDestination
	}
	if u.Scheme != "https" && !t.private[strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))] && carriesOutboundSecrets(request) {
		return nil, errors.New("authenticated outbound requests require HTTPS")
	}
	return t.base.RoundTrip(request)
}
func (t *publicTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }
