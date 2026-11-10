package httpclient

import "net/http"

// PublicMediaClient retains the underlying outbound policy and HTTP budget while
// removing connection-specific API rewrites and credentials. Callers must build
// a fresh unauthenticated request and validate the media URL and redirects.
func PublicMediaClient(client *http.Client) *http.Client {
	copy := *client
	if transport, ok := copy.Transport.(interface {
		PublicMediaTransport() http.RoundTripper
		PublicMediaRedirectPolicy() func(*http.Request, []*http.Request) error
	}); ok {
		copy.Transport = transport.PublicMediaTransport()
		copy.CheckRedirect = transport.PublicMediaRedirectPolicy()
	}
	copy.Jar = nil
	return &copy
}
