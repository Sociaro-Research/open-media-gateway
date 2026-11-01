package references

import (
	"crypto/hmac"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// ParseSignedReference identifies a storage capability, never a caller-selected
// network source. Extra query values and the supplied origin are not part of the
// capability and must not be forwarded when resolving it to a provider URL.
func ParseSignedReference(raw string, secret []byte) (owner, id string, ok bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.RawPath != "" || len(secret) == 0 {
		return "", "", false
	}
	parts := strings.Split(parsed.Path, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "references" {
		return "", "", false
	}
	owner, id = parts[2], parts[3]
	if _, err := uuid.Parse(owner); err != nil {
		return "", "", false
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", "", false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(query["signature"]) != 1 || !hmac.Equal([]byte(query.Get("signature")), []byte(Signature(secret, owner, id))) {
		return "", "", false
	}
	return owner, id, true
}

// CanonicalReferenceURL binds a capability to the operator-configured Gateway
// origin. Incoming request hosts, paths and query values cannot select its bytes.
func CanonicalReferenceURL(origin string, secret []byte, owner, id string) (string, error) {
	base, err := url.Parse(origin)
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || (base.Path != "" && base.Path != "/") || base.RawPath != "" || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.Opaque != "" || len(secret) == 0 {
		return "", fmt.Errorf("canonical Gateway reference origin is unavailable")
	}
	base.Path = "/references/" + owner + "/" + id
	base.RawQuery = url.Values{"signature": {Signature(secret, owner, id)}}.Encode()
	return base.String(), nil
}
