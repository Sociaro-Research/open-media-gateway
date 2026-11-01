package references

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
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
	origin  string
}

func NewResolver(library ImageOpener, secret string, canonicalOrigin ...string) *Resolver {
	r := &Resolver{library: library, secret: []byte(secret)}
	if len(canonicalOrigin) > 0 {
		r.origin = canonicalOrigin[0]
	}
	return r
}

// Resolve reads signed references from owner-scoped storage, never from a supplied
// host. External URLs remain provider-fetched. Keep the encoded payload bounded;
// references beyond this budget use the configured canonical capability URL.
func (r *Resolver) Resolve(ctx context.Context, images []string) ([]string, error) {
	return r.resolve(ctx, images, true)
}
func (r *Resolver) resolve(ctx context.Context, images []string, inline bool) ([]string, error) {
	result := append([]string(nil), images...)
	remaining := 48 << 20
	directCache := map[string]string{}
	for i, raw := range images {
		owner, id, ok := ParseSignedReference(raw, r.secret)
		if !ok {
			continue
		}
		if owner != identity.OwnerID(ctx) && owner != templates.LibraryOwnerID {
			return nil, fmt.Errorf("reference belongs to another account")
		}
		// Host/query aliases identify the same authorized object. Resolve it
		// once so time-varying direct signatures cannot split one source.
		source := owner + "/" + id
		if cached, ok := directCache[source]; ok {
			result[i] = cached
			continue
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
				directCache[source] = target
				continue
			}
		}
		canonical := func() error {
			target, err := CanonicalReferenceURL(r.origin, r.secret, owner, id)
			if err != nil {
				return err
			}
			result[i] = target
			directCache[source] = target
			return nil
		}
		if !inline {
			if err := canonical(); err != nil {
				return nil, err
			}
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
			if err := canonical(); err != nil {
				return nil, err
			}
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

// ResolveDirect binds local capability URLs to the configured Gateway origin or
// an owner-scoped direct storage URL immediately before provider submission.
// Stable library URLs remain persisted, so queue delays do not expire them.
func (r *Resolver) ResolveDirect(ctx context.Context, o core.MediaOptions) (core.MediaOptions, error) {
	all := append([]string{}, o.ImageURLs...)
	all = append(all, o.VideoURLs...)
	all = append(all, o.AudioURLs...)
	all = append(all, o.VideoURL, o.AudioURL)
	visualKeys := slices.Sorted(maps.Keys(o.ReferenceVoices))
	for _, visual := range visualKeys {
		all = append(all, visual, o.ReferenceVoices[visual])
	}
	resolved, err := r.resolve(ctx, all, false)
	if err != nil {
		return o, err
	}
	replacements := make(map[string]string, len(all))
	capabilityTargets := make(map[string]string)
	for i, u := range all {
		replacements[u] = resolved[i]
		if owner, id, ok := ParseSignedReference(u, r.secret); ok {
			capabilityTargets[owner+"/"+id] = resolved[i]
		}
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
	var voices map[string]string
	if o.ReferenceVoices != nil {
		voices = make(map[string]string, len(o.ReferenceVoices))
		for _, visual := range visualKeys {
			target, voice := replacements[visual], replacements[o.ReferenceVoices[visual]]
			if previous, exists := voices[target]; exists && previous != voice {
				return o, fmt.Errorf("%w: conflicting voices for the same reference", core.ErrOptionsInvalid)
			}
			voices[target] = voice
		}
	}
	var evidence map[string]core.InputMediaEvidence
	if o.ReferenceMediaEvidence != nil {
		evidence = make(map[string]core.InputMediaEvidence, len(o.ReferenceMediaEvidence))
		for _, raw := range slices.Sorted(maps.Keys(o.ReferenceMediaEvidence)) {
			inspected := o.ReferenceMediaEvidence[raw]
			target, replaced := replacements[raw]
			if !replaced {
				target = raw
				if owner, id, ok := ParseSignedReference(raw, r.secret); ok {
					if resolved, exists := capabilityTargets[owner+"/"+id]; exists {
						target = resolved
					}
				}
			}
			if previous, exists := evidence[target]; exists && previous != inspected {
				return o, fmt.Errorf("%w: conflicting evidence for the same reference", core.ErrOptionsInvalid)
			}
			evidence[target] = inspected
		}
	}
	o.ImageURLs = replace(o.ImageURLs)
	o.VideoURLs = replace(o.VideoURLs)
	o.AudioURLs = replace(o.AudioURLs)
	o.VideoURL = replacements[o.VideoURL]
	o.AudioURL = replacements[o.AudioURL]
	o.ReferenceVoices = voices
	// Dispatch uses transient direct URLs; accepted canonical evidence stays
	// untouched while provider validation retains its trusted duration lookup.
	o.ReferenceMediaEvidence = evidence
	return o, nil
}
