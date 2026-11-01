package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Existing image transports accept up to 256 MiB of encoded inline output.
// This private pending envelope adds bounded metadata; public/upload limits do
// not change. Terminal transitions remove it after durable archival.
const (
	MaxAcceptedReceiptInlineBytes   = 256 << 20
	MaxAcceptedReceiptMetadataBytes = 16 << 20
	MaxAcceptedReceiptBytes         = MaxAcceptedReceiptInlineBytes + MaxAcceptedReceiptMetadataBytes
)

var ErrAcceptedReceiptInvalid = errors.New("accepted provider receipt is invalid or does not match the task")

// AcceptedReceiptRepository is private worker persistence. A receipt records an
// already accepted immediate result; it is neither a polling handle nor a public
// task field. A live claim must fence both its creation and recovery.
type AcceptedReceiptRepository interface {
	SaveAcceptedReceipt(context.Context, Task, ProviderTask) error
	LoadAcceptedReceipt(context.Context, Task) (*ProviderTask, error)
}

// AcceptedReceiptProgressRepository records an immutable completed artifact
// prefix separately from the immutable accepted response. A successor must
// verify stored bytes before reusing these paths. A nil last frame is absent.
type AcceptedReceiptProgressRepository interface {
	SaveAcceptedReceiptProgress(context.Context, Task, []StoredArtifact, *StoredArtifact) error
	LoadAcceptedReceiptProgress(context.Context, Task) ([]StoredArtifact, *StoredArtifact, error)
}

type acceptedReceiptBinding struct {
	ID               string `json:"id"`
	OwnerID          string `json:"owner_id"`
	ProviderOwnerID  string `json:"provider_owner_id"`
	StorageOwnerID   string `json:"storage_owner_id"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	ProviderIdentity string `json:"provider_identity"`
	MediaType        string `json:"media_type"`
	ActorID          string `json:"actor_id"`
}
type acceptedReceiptEnvelope struct {
	Version            int                    `json:"version"`
	PrimaryArtifactURL bool                   `json:"primary_artifact_url,omitempty"`
	Binding            acceptedReceiptBinding `json:"binding"`
	Result             ProviderTask           `json:"result"`
}

func acceptedBinding(t Task) acceptedReceiptBinding {
	return acceptedReceiptBinding{ID: t.ID, OwnerID: t.OwnerID, ProviderOwnerID: t.ProviderOwnerID, StorageOwnerID: t.StorageOwnerID, Provider: t.Provider, Model: t.Model, ProviderIdentity: t.ProviderIdentity, MediaType: t.MediaType, ActorID: t.ActorID}
}
func validAcceptedBinding(t Task) bool {
	for _, s := range []string{t.ID, t.OwnerID, t.ProviderOwnerID, t.StorageOwnerID, t.Provider, t.Model, t.MediaType} {
		if s == "" || len(s) > 4096 || !utf8.ValidString(s) {
			return false
		}
	}
	return len(t.ActorID) <= 4096 && utf8.ValidString(t.ActorID) && len(t.ProviderIdentity) <= 4096 && utf8.ValidString(t.ProviderIdentity)
}

// EncodeAcceptedReceipt returns a bounded, task-bound private envelope and the
// digest of its exact bytes. It never places media payloads in an operation ID.
func EncodeAcceptedReceipt(t Task, p ProviderTask) ([]byte, string, error) {
	budget, valid := validateAcceptedProviderTask(p)
	if !validAcceptedBinding(t) || valid != nil {
		return nil, "", ErrAcceptedReceiptInvalid
	}
	alias := len(p.Artifacts) > 0 && p.ResultURL != "" && p.ResultURL == p.Artifacts[0].URL
	stored := p
	if alias {
		stored.ResultURL = ""
	}
	raw, e := json.Marshal(acceptedReceiptEnvelope{Version: 1, Binding: acceptedBinding(t), Result: stored, PrimaryArtifactURL: alias})
	if e != nil || len(raw) > MaxAcceptedReceiptBytes || len(raw)-budget.inline > MaxAcceptedReceiptMetadataBytes {
		return nil, "", ErrAcceptedReceiptInvalid
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}
func DecodeAcceptedReceipt(t Task, raw []byte, digest string) (*ProviderTask, error) {
	if !validAcceptedBinding(t) || len(raw) == 0 || len(raw) > MaxAcceptedReceiptBytes || len(digest) != 64 {
		return nil, ErrAcceptedReceiptInvalid
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != digest {
		return nil, ErrAcceptedReceiptInvalid
	}
	var envelope acceptedReceiptEnvelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || decoder.Decode(new(any)) != io.EOF || envelope.Version != 1 || envelope.Binding != acceptedBinding(t) {
		return nil, ErrAcceptedReceiptInvalid
	}
	if envelope.PrimaryArtifactURL {
		if envelope.Result.ResultURL != "" || len(envelope.Result.Artifacts) == 0 || envelope.Result.Artifacts[0].URL == "" {
			return nil, ErrAcceptedReceiptInvalid
		}
		envelope.Result.ResultURL = envelope.Result.Artifacts[0].URL
	}
	budget, e := validateAcceptedProviderTask(envelope.Result)
	if e != nil || len(raw)-budget.inline > MaxAcceptedReceiptMetadataBytes {
		return nil, ErrAcceptedReceiptInvalid
	}
	return &envelope.Result, nil
}
func receiptFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

type receiptBudget struct{ metadata, inline int }

func receiptString(s string, max int, budget *receiptBudget) bool {
	if len(s) > max || !utf8.ValidString(s) {
		return false
	}
	encoded := 2
	for _, r := range s {
		switch r {
		case '\n', '\r', '\t', '\b', '\f', '"', '\\':
			encoded += 2
		case '<', '>', '&', '\u2028', '\u2029':
			encoded += 6
		default:
			if r < 32 {
				encoded += 6
			} else {
				encoded += utf8.RuneLen(r)
			}
		}
		if encoded > MaxAcceptedReceiptMetadataBytes-budget.metadata {
			return false
		}
	}
	budget.metadata += encoded
	return budget.metadata <= MaxAcceptedReceiptMetadataBytes
}
func receiptURL(s string, budget *receiptBudget) bool {
	if strings.HasPrefix(s, "data:") {
		if len(s) > MaxAcceptedReceiptInlineBytes-budget.inline {
			return false
		}
		header, payload, ok := strings.Cut(s, ",")
		if !ok || len(header) > 256 || !strings.HasSuffix(header, ";base64") || payload == "" {
			return false
		}
		typ := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
		if !strings.HasPrefix(typ, "image/") && !strings.HasPrefix(typ, "video/") && !strings.HasPrefix(typ, "audio/") {
			return false
		}
		for _, r := range typ {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '/' || r == '-' || r == '.' || r == '+') {
				return false
			}
		}
		if _, e := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding, strings.NewReader(payload))); e != nil {
			return false
		}
		budget.inline += len(s)
		return true
	}
	if !receiptString(s, 16384, budget) {
		return false
	}
	u, e := url.Parse(s)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

// ValidateAcceptedProviderTask bounds the normalized result, including inline
// media/text and recursive resources, before JSON encoding can allocate from an
// unbounded provider response. Provider-specific semantics stay in adapters.
func ValidateAcceptedProviderTask(p ProviderTask) error {
	_, e := validateAcceptedProviderTask(p)
	return e
}
func validateAcceptedProviderTask(p ProviderTask) (receiptBudget, error) {
	budget := receiptBudget{}
	if !receiptString(p.ID, 4096, &budget) || strings.HasPrefix(p.ID, "data:") || len(p.Artifacts) > 64 || len(p.Resources) > 64 || (len(p.Artifacts) == 0 && p.ResultURL == "" && len(p.Resources) == 0) {
		return budget, ErrAcceptedReceiptInvalid
	}
	if p.ResultURL != "" && !(len(p.Artifacts) > 0 && p.ResultURL == p.Artifacts[0].URL) && !receiptURL(p.ResultURL, &budget) || p.LastFrameURL != "" && !receiptURL(p.LastFrameURL, &budget) {
		return budget, ErrAcceptedReceiptInvalid
	}
	for _, a := range p.Artifacts {
		if !receiptString(a.ID, 1024, &budget) || !receiptString(a.Format, 64, &budget) || !receiptString(a.GroupID, 256, &budget) || !receiptFinite(a.Duration) {
			return budget, ErrAcceptedReceiptInvalid
		}
		switch a.MediaType {
		case "image", "video", "audio", "speech", "music", "text", "json", "binary":
		default:
			return budget, ErrAcceptedReceiptInvalid
		}
		n := 0
		for _, s := range []string{a.URL, a.SourceReference, a.Text} {
			if s != "" {
				n++
			}
		}
		if n != 1 {
			return budget, ErrAcceptedReceiptInvalid
		}
		if a.URL != "" && !receiptURL(a.URL, &budget) || a.SourceReference != "" && !receiptString(a.SourceReference, 65536, &budget) || a.Text != "" && (!receiptString(a.Text, MaxAcceptedReceiptBytes, &budget) || (a.MediaType != "text" && a.MediaType != "json")) || a.MediaType == "json" && a.Text != "" && !json.Valid([]byte(a.Text)) || a.MediaType == "binary" && (a.SourceReference == "" || a.URL != "" || a.Text != "") {
			return budget, ErrAcceptedReceiptInvalid
		}
	}
	if len(p.Artifacts) > 0 && p.ResultURL != "" && p.ResultURL != p.Artifacts[0].URL {
		return budget, ErrAcceptedReceiptInvalid
	}
	nodes := 0
	for i := range p.Resources {
		if !receiptResource(&p.Resources[i], &nodes, &budget) {
			return budget, ErrAcceptedReceiptInvalid
		}
	}
	if u := p.Usage; u != nil {
		if len(u.Metrics) > 256 || len(u.Raw) > MaxAcceptedReceiptMetadataBytes || u.EstimatedCost != nil && !receiptFinite(*u.EstimatedCost) || !receiptString(u.Currency, 32, &budget) || !receiptString(u.CostStatus, 128, &budget) || !receiptString(u.CostSource, 4096, &budget) {
			return budget, ErrAcceptedReceiptInvalid
		}
		budget.metadata += len(u.Raw)
		if budget.metadata > MaxAcceptedReceiptMetadataBytes || len(u.Raw) > 0 && !json.Valid(u.Raw) {
			return budget, ErrAcceptedReceiptInvalid
		}
		for _, m := range u.Metrics {
			if !receiptString(m.Name, 256, &budget) || m.Name == "" || !receiptString(m.Unit, 128, &budget) || !receiptFinite(m.Quantity) {
				return budget, ErrAcceptedReceiptInvalid
			}
		}
	}
	return budget, nil
}
func receiptResource(r *ProviderResource, nodes *int, budget *receiptBudget) bool {
	*nodes++
	if *nodes > 64 || r.Status != ProviderSucceeded || r.IsDeleted || r.RemoteID == "" || r.Kind == "" || len(r.Sources) > 64 || len(r.Tags) > 64 {
		return false
	}
	for _, s := range []struct {
		value string
		limit int
	}{{r.RemoteID, 4096}, {r.Kind, 128}, {r.Subtype, 256}, {r.Name, 4096}, {r.Description, 65536}} {
		if !receiptString(s.value, s.limit, budget) {
			return false
		}
	}
	if r.PreviewURL != "" && !receiptURL(r.PreviewURL, budget) {
		return false
	}
	for _, tag := range r.Tags {
		if !receiptString(tag, 1024, budget) {
			return false
		}
	}
	for _, s := range r.Sources {
		if len(s.AudioCodecs) > 256 {
			return false
		}
		for _, codec := range s.AudioCodecs {
			if !receiptString(codec, 128, budget) {
				return false
			}
		}
		for _, field := range []struct {
			value string
			limit int
		}{{s.ReferenceID, 4096}, {s.MediaType, 128}, {s.ContentType, 256}, {s.SHA256, 128}} {
			if !receiptString(field.value, field.limit, budget) {
				return false
			}
		}
		if s.URL != "" && !receiptURL(s.URL, budget) || s.SizeBytes < 0 || s.Width < 0 || s.Height < 0 || s.FrameCount < 0 || !receiptFinite(s.FPS) || s.Duration != nil && !receiptFinite(*s.Duration) || s.AudioStreams != nil && *s.AudioStreams < 0 || s.VideoStreams != nil && *s.VideoStreams < 0 {
			return false
		}
	}
	if e := r.Session; e != nil {
		if len(e.Faces) > 64 || len(e.Selections) > 10 || !receiptFinite(e.SourceVideoDuration) || !receiptFinite(e.FPS) || e.FrameCount < 0 || e.SelectionRevision < 0 || !receiptString(e.SourceSHA256, 128, budget) {
			return false
		}
		for _, f := range e.Faces {
			if !receiptString(f.ID, 4096, budget) || !receiptString(f.PreviewArtifactID, 1024, budget) || f.StartMS < 0 || f.EndMS < f.StartMS {
				return false
			}
		}
		for _, f := range e.Selections {
			if f.FrameIndex < 0 || len(f.Points) > 10 {
				return false
			}
			for _, p := range f.Points {
				if !receiptFinite(p.X) || !receiptFinite(p.Y) || p.X > 1 || p.Y > 1 {
					return false
				}
			}
		}
	}
	return r.BoundVoice == nil || receiptResource(r.BoundVoice, nodes, budget)
}
