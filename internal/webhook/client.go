package webhook

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/webhookurl"
)

type lookupIPs func(context.Context, string, string) ([]netip.Addr, error)
type dialIP func(context.Context, string, string) (net.Conn, error)

// NewHTTPClient enforces the policy on every new connection, including retries
// of deliveries persisted before this policy existed. Do not use an environment
// proxy: it would resolve/connect outside our checked-IP boundary.
func NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return newHTTPClient(timeout, net.DefaultResolver.LookupNetIP, dialer.DialContext)
}

func newHTTPClient(timeout time.Duration, lookup lookupIPs, dial dialIP) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = publicDial(lookup, dial)
	transport.DialTLSContext = nil
	return &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func publicDial(lookup lookupIPs, dial dialIP) dialIP {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, webhookurl.ErrDestination
		}
		var ips []netip.Addr
		if ip, err := netip.ParseAddr(host); err == nil {
			ips = []netip.Addr{ip}
		} else {
			ips, err = lookup(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("resolve webhook destination: %w", err)
			}
		}
		if len(ips) == 0 {
			return nil, webhookurl.ErrDestination
		}
		// Reject mixed public/private answers before opening any connection.
		for _, ip := range ips {
			if !webhookurl.PublicIP(ip) {
				return nil, webhookurl.ErrDestination
			}
		}
		for _, ip := range ips {
			// Dial the checked literal, never the hostname (no second DNS lookup).
			var conn net.Conn
			conn, err = dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
			if err == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, err
	}
}
