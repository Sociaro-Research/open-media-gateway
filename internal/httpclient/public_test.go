package httpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicClientRejectsPrivateAndSpecialUseDestinations(t *testing.T) {
	for _, target := range []string{
		"http://127.0.0.1/", "http://localhost/", "http://10.0.0.1/",
		"http://169.254.169.254/latest/meta-data/", "http://168.63.129.16/",
		"http://100.64.0.1/", "http://192.168.1.2/", "http://[::1]/",
		"http://[::ffff:127.0.0.1]/", "http://[fe80::1]/", "http://[fd00::1]/",
		"http://private.example/", "http://mixed.example/", "http://empty.example/",
		"http://user:secret@public.example/", "http://public.example/#fragment",
		"file:///etc/passwd",
	} {
		t.Run(target, func(t *testing.T) {
			lookup := func(_ context.Context, _, host string) ([]netip.Addr, error) {
				switch host {
				case "mixed.example":
					return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.0.0.1")}, nil
				case "empty.example":
					return nil, nil
				default:
					return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
				}
			}
			client := newPublic(time.Second, nil, lookup, func(context.Context, string, string) (net.Conn, error) {
				t.Error("forbidden destination reached the network")
				return nil, errors.New("unexpected dial")
			})
			_, err := client.Get(target)
			if !errors.Is(err, ErrOutboundDestination) {
				t.Fatalf("expected destination rejection, got %v", err)
			}
		})
	}
}

func TestPublicClientPinsCheckedDNSAddressAndIgnoresEnvironmentProxy(t *testing.T) {
	var lookups atomic.Int32
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:8080")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "public.example" {
			t.Errorf("host identity changed: %q", r.Host)
		}
		w.Write([]byte("fixture"))
	}))
	defer server.Close()
	client := newPublic(time.Second, nil, func(context.Context, string, string) ([]netip.Addr, error) {
		if lookups.Add(1) > 1 {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "8.8.8.8:80" {
			t.Errorf("dial did not use checked literal: %q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	defer client.CloseIdleConnections()
	response, err := client.Get("http://public.example/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if string(data) != "fixture" || lookups.Load() != 1 {
		t.Fatalf("checked destination was not pinned: lookups=%d body=%q", lookups.Load(), data)
	}
}

func TestPublicClientRedirectDoesNotLeakHeadersOrBodies(t *testing.T) {
	for _, kind := range []string{"media", "authorization", "custom-header", "post-body", "private-redirect"} {
		t.Run(kind, func(t *testing.T) {
			var sink atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/start" {
					target := "https://cdn.example/finish"
					if kind == "private-redirect" {
						target = "http://169.254.169.254/finish"
					}
					http.Redirect(w, r, target, http.StatusTemporaryRedirect)
					return
				}
				sink.Add(1)
				w.Write([]byte("media"))
			}))
			defer server.Close()
			client := newPublic(time.Second, nil, func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}, func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			})
			defer client.CloseIdleConnections()
			// Only the controlled local TLS fixture bypasses certificate verification.
			client.Transport.(*publicTransport).base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
			request, _ := http.NewRequest("GET", "https://public.example/start", nil)
			switch kind {
			case "authorization":
				request.Header.Set("Authorization", "Bearer private")
			case "custom-header":
				request.Header.Set("X-API-Key", "private")
			case "post-body":
				request, _ = http.NewRequest("POST", "https://public.example/start", strings.NewReader("private"))
			}
			response, err := client.Do(request)
			if response != nil {
				response.Body.Close()
			}
			if kind == "media" {
				if err != nil || sink.Load() != 1 {
					t.Fatalf("public media redirect failed: %v, sink=%d", err, sink.Load())
				}
			} else if err == nil || sink.Load() != 0 {
				t.Fatalf("unsafe redirect reached sink: %v, sink=%d", err, sink.Load())
			}
		})
	}
}

func TestPublicClientDoesNotSendCredentialsOverPlainHTTP(t *testing.T) {
	for _, kind := range []string{"authorization", "custom-header", "post-body"} {
		t.Run(kind, func(t *testing.T) {
			client := newPublic(time.Second, nil, func(context.Context, string, string) ([]netip.Addr, error) {
				t.Error("resolved an insecure credential receiver")
				return nil, nil
			}, func(context.Context, string, string) (net.Conn, error) {
				t.Error("dialed an insecure credential receiver")
				return nil, errors.New("unexpected dial")
			})
			request, _ := http.NewRequest("GET", "http://public.example/", nil)
			switch kind {
			case "authorization":
				request.Header.Set("Authorization", "Bearer fixture")
			case "custom-header":
				request.Header.Set("X-API-Key", "fixture")
			case "post-body":
				request, _ = http.NewRequest("POST", "http://public.example/", strings.NewReader("fixture"))
			}
			if _, err := client.Do(request); err == nil || !strings.Contains(err.Error(), "HTTPS") {
				t.Fatal("credentials were allowed over HTTP", err)
			}
		})
	}
}

func TestPublicClientPrivateExceptionIsExactAndOperatorSupplied(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	client := newPublic(time.Second, []string{"TRUSTED.INTERNAL."}, func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	defer client.CloseIdleConnections()
	for _, host := range []string{"trusted.internal", "other.internal", "evil.trusted.internal"} {
		response, err := client.Get("http://" + host + "/")
		if response != nil {
			response.Body.Close()
		}
		if (err == nil) != (host == "trusted.internal") {
			t.Fatalf("incorrect private-host exception for %s: %v", host, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatal("exception reached other private hosts", hits.Load())
	}
}
