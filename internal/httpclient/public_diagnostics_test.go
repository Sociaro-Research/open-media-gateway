package httpclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPublicClientClassifiesBlockedAndDNSDestinations(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint string
		ips            []netip.Addr
		lookupErr      error
		want           error
	}{
		{name: "private literal", endpoint: "https://10.0.0.1", want: ErrOutboundPrivateDestination},
		{name: "private IPv6", endpoint: "https://[fd00::1]", want: ErrOutboundPrivateDestination},
		{name: "loopback name", endpoint: "https://localhost", want: ErrOutboundPrivateDestination},
		{name: "metadata", endpoint: "https://169.254.169.254", want: ErrOutboundPrivateDestination},
		{name: "resolved private", endpoint: "https://storage.example", ips: []netip.Addr{netip.MustParseAddr("10.0.0.1")}, want: ErrOutboundPrivateDestination},
		{name: "mixed DNS", endpoint: "https://storage.example", ips: []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.0.0.1")}, want: ErrOutboundPrivateDestination},
		{name: "DNS error", endpoint: "https://storage.example", lookupErr: &net.DNSError{Name: "fixture-secret", Server: "private-resolver", Err: "raw signed URL"}, want: ErrOutboundDNS},
		{name: "empty DNS", endpoint: "https://storage.example", want: ErrOutboundDNS},
		{name: "userinfo", endpoint: "https://user:fixture-secret@storage.example", want: ErrOutboundDestination},
		{name: "fragment", endpoint: "https://storage.example/#fixture-secret", want: ErrOutboundDestination},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialed := false
			client := newPublic(time.Second, nil, func(context.Context, string, string) ([]netip.Addr, error) { return tc.ips, tc.lookupErr }, func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("unexpected dial")
			})
			_, err := client.Get(tc.endpoint)
			if !errors.Is(err, tc.want) || !errors.Is(err, ErrOutboundDestination) || dialed {
				t.Fatalf("classification=%v dialed=%t", err, dialed)
			}
			var wrapped *url.Error
			if !errors.As(err, &wrapped) {
				t.Fatal("expected HTTP wrapper")
			}
			if tc.want == ErrOutboundDestination && (errors.Is(err, ErrOutboundDNS) || errors.Is(err, ErrOutboundPrivateDestination)) {
				t.Fatal("invalid URL misclassified")
			}
			for _, detail := range []string{"fixture-secret", "private-resolver", "raw signed URL"} {
				if strings.Contains(wrapped.Err.Error(), detail) {
					t.Fatal("classified cause leaked resolver/request detail")
				}
			}
		})
	}
}

func TestPublicClientCancelledDNSIsNotResolutionFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := newPublic(time.Second, nil, func(context.Context, string, string) ([]netip.Addr, error) { return nil, context.Canceled }, func(context.Context, string, string) (net.Conn, error) { t.Fatal("unexpected dial"); return nil, nil })
	// Exercise the dial boundary directly; net/http may reject cancellation first.
	_, err := client.Transport.(*publicTransport).base.DialContext(ctx, "tcp", "storage.example:443")
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrOutboundDNS) {
		t.Fatalf("cancellation mislabeled: %v", err)
	}
}

func TestPublicClientHTTPSGuardPrecedesDNSDiagnostics(t *testing.T) {
	lookedUp := false
	client := newPublic(time.Second, nil, func(context.Context, string, string) ([]netip.Addr, error) {
		lookedUp = true
		return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
	}, func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("insecure credential request dialed")
		return nil, nil
	})
	request, _ := http.NewRequest("GET", "http://storage.example", nil)
	request.Header.Set("Authorization", "synthetic-credential")
	_, err := client.Do(request)
	if err == nil || !strings.Contains(err.Error(), "HTTPS") || lookedUp || errors.Is(err, ErrOutboundDNS) || errors.Is(err, ErrOutboundPrivateDestination) {
		t.Fatalf("HTTPS enforcement changed: %v lookup=%t", err, lookedUp)
	}
}
