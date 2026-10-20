package webhook

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/webhookurl"
)

func TestWebhookDNSBoundary(t *testing.T) {
	for _, addresses := range [][]string{{"127.0.0.1"}, {"::ffff:169.254.169.254"}, {"8.8.8.8", "10.0.0.1"}, {"2606:4700:4700::1111", "::1"}, {}} {
		t.Run(fmt.Sprint(addresses), func(t *testing.T) {
			called := false
			lookup := func(context.Context, string, string) ([]netip.Addr, error) {
				var ips []netip.Addr
				for _, ip := range addresses {
					ips = append(ips, netip.MustParseAddr(ip))
				}
				return ips, nil
			}
			dial := publicDial(lookup, func(context.Context, string, string) (net.Conn, error) {
				called = true
				return nil, errors.New("unexpected dial")
			})
			_, err := dial(context.Background(), "tcp", "attacker.example:443")
			if !errors.Is(err, webhookurl.ErrDestination) || called {
				t.Fatalf("err=%v dialed=%v", err, called)
			}
		})
	}
}

func TestWebhookPinsDNSAndRechecksNextConnection(t *testing.T) {
	lookups, dials := 0, 0
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		lookups++
		if lookups > 1 {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	dial := publicDial(lookup, func(_ context.Context, _, address string) (net.Conn, error) {
		dials++
		if address != "8.8.8.8:443" {
			t.Errorf("unpinned address: %s", address)
		}
		return nil, errors.New("fixture dial")
	})
	_, _ = dial(context.Background(), "tcp", "rebind.example:443")
	_, err := dial(context.Background(), "tcp", "rebind.example:443")
	if !errors.Is(err, webhookurl.ErrDestination) || lookups != 2 || dials != 1 {
		t.Fatalf("%v lookups=%d dials=%d", err, lookups, dials)
	}
}

// Exercise the actual HTTP transport without contacting any external service.
func TestWebhookPublicHTTPAndRedirectPolicy(t *testing.T) {
	for _, status := range []int{204, 302, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			received := make(chan *http.Request, 1)
			client := newHTTPClient(time.Second,
				func(context.Context, string, string) ([]netip.Addr, error) {
					return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
				},
				func(_ context.Context, _, address string) (net.Conn, error) {
					if address != "8.8.8.8:80" {
						return nil, fmt.Errorf("unexpected destination %s", address)
					}
					app, server := net.Pipe()
					go func() {
						defer server.Close()
						r, err := http.ReadRequest(bufio.NewReader(server))
						if err != nil {
							return
						}
						_, _ = io.Copy(io.Discard, r.Body)
						received <- r
						fmt.Fprintf(server, "HTTP/1.1 %d Status\r\nLocation: http://127.0.0.1/private\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", status)
					}()
					return app, nil
				})
			defer client.CloseIdleConnections()
			transport := client.Transport.(*http.Transport)
			if transport.Proxy != nil {
				t.Fatal("proxy can bypass destination validation")
			}
			err := NewSender("secret", client).Send(context.Background(), core.WebhookDelivery{URL: "http://customer.example/events", Payload: []byte(`{"event":"done"}`)})
			if (err == nil) != (status == 204) {
				t.Fatalf("delivery result: %v", err)
			}
			if status != 204 && !strings.Contains(err.Error(), fmt.Sprintf("webhook returned HTTP %d", status)) {
				t.Fatalf("redirect was followed instead of returning the original status: %v", err)
			}
			select {
			case r := <-received:
				if r.Host != "customer.example" || r.Method != "POST" || r.Header.Get("X-Media-Gateway-Signature") == "" {
					t.Fatal("request semantics changed")
				}
			default:
				t.Fatal("public delivery did not arrive")
			}
		})
	}
}

func TestWebhookTLSPreservesServerName(t *testing.T) {
	serverName := make(chan string, 1)
	client := newHTTPClient(time.Second,
		func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		func(_ context.Context, _, address string) (net.Conn, error) {
			if address != "8.8.8.8:443" {
				return nil, errors.New("unpinned TLS address")
			}
			a, b := net.Pipe()
			go func() {
				defer b.Close()
				s := tls.Server(b, &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
					serverName <- hello.ServerName
					return nil, errors.New("stop after ClientHello")
				}})
				_ = s.Handshake()
			}()
			return a, nil
		})
	defer client.CloseIdleConnections()
	_, _ = client.Get("https://customer.example/hook")
	select {
	case name := <-serverName:
		if name != "customer.example" {
			t.Fatal(name)
		}
	default:
		t.Fatal("missing TLS handshake")
	}
}

func TestPreviouslyQueuedPrivateWebhookIsRejected(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("private request sent"); return nil, nil })}
	err := NewSender("secret", client).Send(context.Background(), core.WebhookDelivery{URL: "http://169.254.169.254/latest/meta-data"})
	if !errors.Is(err, webhookurl.ErrDestination) {
		t.Fatal(err)
	}
}
