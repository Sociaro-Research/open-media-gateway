package references

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

// Signature identifies a Gateway reference independently of its public hostname.
func Signature(secret []byte, owner, id string) string {
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "reference:%s:%s\n", owner, id)
	return hex.EncodeToString(mac.Sum(nil))
}

type ImageOpener interface {
	Open(context.Context, string) (Image, storage.Object, error)
}

type Resolver struct {
	library ImageOpener
	secret  []byte
}

func NewResolver(library ImageOpener, secret string) *Resolver {
	return &Resolver{library: library, secret: []byte(secret)}
}

// Resolve reads signed references from owner-scoped storage, never from a supplied
// host. External URLs remain provider-fetched. Keep the encoded payload bounded;
// references beyond this budget retain their original capability URLs.
func (r *Resolver) Resolve(ctx context.Context, images []string) ([]string, error) {
	return r.resolve(ctx, images, true)
}
func (r *Resolver) resolve(ctx context.Context, images []string, inline bool) ([]string, error) {
	result := append([]string(nil), images...)
	remaining := 48 << 20
	directCache := map[string]string{}
	for i, raw := range images {
		if cached, ok := directCache[raw]; ok {
			result[i] = cached
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
		if len(parts) != 3 || parts[0] != "references" {
			continue
		}
		owner, id := parts[1], parts[2]
		if _, err := uuid.Parse(owner); err != nil {
			continue
		}
		if _, err := uuid.Parse(id); err != nil {
			continue
		}
		if len(r.secret) == 0 || !hmac.Equal([]byte(parsed.Query().Get("signature")), []byte(Signature(r.secret, owner, id))) {
			continue
		}
		if owner != identity.OwnerID(ctx) && owner != templates.LibraryOwnerID {
			return nil, fmt.Errorf("reference belongs to another account")
		}
		readContext := ctx
		if owner == templates.LibraryOwnerID {
			readContext = identity.WithOrganization(ctx, "", false)
			readContext = identity.WithDataOwnerID(readContext, owner)
			readContext = identity.WithResourceOwners(readContext, identity.ProviderOwnerID(ctx), identity.SharedOwnerID)
		}
		if direct, ok := r.library.(interface {
			DirectReferenceURL(context.Context, string) (string, error)
		}); ok {
			target, err := direct.DirectReferenceURL(readContext, id)
			if err != nil {
				return nil, err
			}
			if target != "" {
				result[i] = target
				directCache[raw] = target
				continue
			}
		}
		if !inline {
			continue
		}
		item, object, err := r.library.Open(readContext, id)
		if err != nil {
			return nil, fmt.Errorf("read reference %d from storage: %w", i+1, err)
		}
		if item.SizeBytes > MaxBytes || item.SizeBytes < 0 {
			object.Reader.Close()
			return nil, ErrInvalidImage
		}
		if base64.StdEncoding.EncodedLen(int(item.SizeBytes))+64 > remaining {
			object.Reader.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(object.Reader, MaxBytes+1))
		object.Reader.Close()
		if err != nil {
			return nil, fmt.Errorf("read reference %d: %w", i+1, err)
		}
		if int64(len(data)) != item.SizeBytes || len(data) == 0 {
			return nil, ErrInvalidImage
		}
		switch item.ContentType {
		case "image/jpeg", "image/png", "image/webp", "image/gif":
		default:
			return nil, ErrInvalidImage
		}
		result[i] = "data:" + item.ContentType + ";base64," + base64.StdEncoding.EncodeToString(data)
		remaining -= len(result[i])
	}
	return result, nil
}

// ResolveDirect signs S3 references immediately before provider submission. Stable
// library URLs remain in persisted tasks, so queue delays and retries do not expire them.
func (r *Resolver) ResolveDirect(ctx context.Context, o core.MediaOptions) (core.MediaOptions, error) {
	all := append([]string{}, o.ImageURLs...)
	all = append(all, o.VideoURLs...)
	all = append(all, o.AudioURLs...)
	all = append(all, o.VideoURL, o.AudioURL)
	for visual, voice := range o.ReferenceVoices {
		all = append(all, visual, voice)
	}
	resolved, err := r.resolve(ctx, all, false)
	if err != nil {
		return o, err
	}
	replacements := make(map[string]string, len(all))
	for i, u := range all {
		replacements[u] = resolved[i]
	}
	replace := func(values []string) []string {
		if values == nil {
			return nil
		}
		out := make([]string, len(values))
		for i, u := range values {
			out[i] = replacements[u]
		}
		return out
	}
	o.ImageURLs = replace(o.ImageURLs)
	o.VideoURLs = replace(o.VideoURLs)
	o.AudioURLs = replace(o.AudioURLs)
	o.VideoURL = replacements[o.VideoURL]
	o.AudioURL = replacements[o.AudioURL]
	if o.ReferenceVoices != nil {
		voices := make(map[string]string, len(o.ReferenceVoices))
		for visual, voice := range o.ReferenceVoices {
			voices[replacements[visual]] = replacements[voice]
		}
		o.ReferenceVoices = voices
	}
	return o, nil
}
