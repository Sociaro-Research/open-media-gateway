package api

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/google/uuid"
)

const presetTicketTTL = 5 * time.Minute
const presetTicketLimit = 16384
const presetTicketDomain = "gateway/official-resource-preset/v1"

type resourcePresetView struct {
	Name              string     `json:"name"`
	Kind              string     `json:"kind"`
	Subtype           string     `json:"subtype"`
	Importable        bool       `json:"importable"`
	UnavailableReason string     `json:"unavailable_reason,omitempty"`
	ImportTicket      string     `json:"import_ticket,omitempty"`
	TicketExpiresAt   *time.Time `json:"ticket_expires_at,omitempty"`
}

// This encrypted payload is never returned as JSON or accepted from a caller.
// It contains only the minimal immutable official descriptor, no source URLs.
type resourcePresetTicket struct {
	Version         int    `json:"version"`
	ExpiresAt       int64  `json:"expires_at"`
	OwnerID         string `json:"owner"`
	ProviderOwnerID string `json:"provider_owner"`
	ProviderID      string `json:"provider"`
	WorkflowModel   string `json:"workflow_model"`
	ConnectionID    string `json:"connection"`
	Revision        string `json:"revision"`
	Kind            string `json:"kind"`
	Subtype         string `json:"subtype"`
	Name            string `json:"name"`
	RemoteID        string `json:"remote_id"`
}

func (s *Server) registerProviderPresetRoutes(mux *http.ServeMux) {
	mux.Handle("GET /v1/provider-resource-presets", s.providerResourceRoute(s.listResourcePresets))
	mux.Handle("POST /v1/provider-resource-presets/import", s.providerResourceRoute(s.importResourcePreset))
}
func (s *Server) presetCipher() (cipher.AEAD, error) {
	if s.mediaSigner == nil || len(s.mediaSigner.secret) == 0 {
		return nil, providerresources.ErrInvalid
	}
	key := sha256.Sum256(append([]byte(presetTicketDomain+"\x00"), s.mediaSigner.secret...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (s *Server) sealPresetTicket(ticket resourcePresetTicket) (string, error) {
	aead, err := s.presetCipher()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(ticket)
	if err != nil || len(raw) > presetTicketLimit/2 {
		return "", providerresources.ErrInvalid
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, raw, []byte(presetTicketDomain))), nil
}
func (s *Server) openPresetTicket(token string, now time.Time) (resourcePresetTicket, error) {
	var ticket resourcePresetTicket
	if token == "" || len(token) > presetTicketLimit {
		return ticket, providerresources.ErrInvalid
	}
	aead, err := s.presetCipher()
	if err != nil {
		return ticket, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead() {
		return ticket, providerresources.ErrInvalid
	}
	raw, err = aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(presetTicketDomain))
	if err != nil {
		return ticket, providerresources.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&ticket) != nil || decoder.Decode(new(any)) != io.EOF || ticket.Version != 1 || ticket.WorkflowModel == "" || ticket.ExpiresAt <= now.Unix() || ticket.ExpiresAt > now.Add(presetTicketTTL).Unix() {
		return resourcePresetTicket{}, providerresources.ErrInvalid
	}
	return ticket, nil
}
func (s *Server) presetConsumer(r *http.Request, provider, workflow, kind, subtype string) (core.MediaModel, bool) {
	if provider == "" || workflow == "" || (kind != "voice" && kind != "element") {
		return core.MediaModel{}, false
	}
	model, err := s.mediaWorkflowConsumer(r, provider, workflow)
	if err != nil {
		return model, false
	}
	for _, schema := range model.ResourceInputSchemas {
		if schema.Kind == kind && schema.MaxCount > 0 && schema.SupportsPresets && (subtype == "" || len(schema.Subtypes) == 0 || slices.Contains(schema.Subtypes, subtype)) {
			return model, true
		}
	}
	return model, false
}
func (s *Server) listResourcePresets(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key, values := range query {
		if (key != "provider" && key != "workflow_model" && key != "kind" && key != "page" && key != "size") || len(values) != 1 {
			invalidParameter(w, key, "Unsupported preset catalogue query.")
			return
		}
	}
	provider, kind, workflow := query.Get("provider"), query.Get("kind"), query.Get("workflow_model")
	consumer, allowed := s.presetConsumer(r, provider, workflow, kind, "")
	if !allowed {
		invalidParameter(w, "provider", "No authorized model accepts official presets of this kind.")
		return
	}
	page, size := 1, 30
	for key, target := range map[string]*int{"page": &page, "size": &size} {
		if raw := query.Get(key); raw != "" {
			n, err := strconv.Atoi(raw)
			maximum := 1000
			if key == "size" {
				maximum = 100
			}
			if err != nil || n < 1 || n > maximum {
				invalidParameter(w, key, "Invalid preset pagination.")
				return
			}
			*target = n
		}
	}
	if s.resourceLifecycle == nil || s.mediaSigner == nil {
		s.resourceError(w, errors.New("preset service unavailable"))
		return
	}
	result, scope, err := s.resourceLifecycle.ListPresets(r.Context(), provider, kind, page, size)
	if err != nil {
		s.resourceError(w, err)
		return
	}
	now := time.Now().UTC()
	expires := time.Unix(now.Add(presetTicketTTL).Unix(), 0).UTC()
	data := []resourcePresetView{}
	seen := map[string]bool{}
	for _, resource := range result.Resources {
		if _, allowed := s.presetConsumer(r, provider, consumer.ID, kind, resource.Subtype); !allowed {
			continue
		}
		if seen[resource.RemoteID] {
			s.resourceError(w, errors.New("duplicate official preset"))
			return
		}
		seen[resource.RemoteID] = true
		item := resourcePresetView{Name: resource.Name, Kind: kind, Subtype: resource.Subtype, Importable: resource.Subtype != "video_character"}
		if !item.Importable {
			item.UnavailableReason = "video_source_inspection_unavailable"
		} else {
			ticket := resourcePresetTicket{Version: 1, ExpiresAt: expires.Unix(), OwnerID: identity.OwnerID(r.Context()), ProviderOwnerID: scope.OwnerID, ProviderID: provider, WorkflowModel: consumer.ID, ConnectionID: scope.ID, Revision: scope.Revision, Kind: kind, Subtype: resource.Subtype, Name: resource.Name, RemoteID: resource.RemoteID}
			token, err := s.sealPresetTicket(ticket)
			if err != nil {
				s.resourceError(w, err)
				return
			}
			item.ImportTicket = token
			item.TicketExpiresAt = &expires
		}
		data = append(data, item)
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeViewerJSON(w, r, 200, map[string]any{"object": "list", "provider": provider, "workflow_model": consumer.ID, "kind": kind, "page": page, "size": size, "has_more": result.HasMore, "data": data})
}
func (s *Server) importResourcePreset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider      string `json:"provider"`
		WorkflowModel string `json:"workflow_model"`
		Ticket        string `json:"ticket"`
	}
	if !decodeSystemLimit(w, r, &body, 32<<10) {
		return
	}
	ticket, err := s.openPresetTicket(body.Ticket, time.Now())
	if err != nil || ticket.OwnerID != identity.OwnerID(r.Context()) || ticket.ProviderOwnerID != identity.ProviderOwnerID(r.Context()) || body.Provider != ticket.ProviderID {
		invalidParameter(w, "ticket", "Invalid or expired official preset ticket.")
		return
	}
	consumer, allowed := s.presetConsumer(r, body.Provider, body.WorkflowModel, ticket.Kind, ticket.Subtype)
	if !allowed || consumer.ID != ticket.WorkflowModel {
		invalidParameter(w, "provider", "No authorized model accepts this official preset.")
		return
	}
	scope, err := s.resourceScopes(r.Context(), body.Provider)
	if err != nil {
		s.resourceError(w, err)
		return
	}
	if scope.ID != ticket.ConnectionID || scope.OwnerID != ticket.ProviderOwnerID || scope.Revision == "" || scope.Revision != ticket.Revision {
		s.resourceError(w, providerresources.ErrNotFound)
		return
	}
	importer, ok := s.resourceRepository.(providerresources.PresetImporter)
	if !ok {
		s.resourceError(w, errors.New("preset imports unavailable"))
		return
	}
	ctx := core.WithProviderConnectionScope(core.WithProviderRevision(r.Context(), scope.Revision), scope)
	b := providerresources.Binding{ID: uuid.NewString(), OwnerID: ticket.OwnerID, ActorID: identity.ActorID(r.Context()), ProviderOwnerID: ticket.ProviderOwnerID, ProviderID: ticket.ProviderID, ConnectionID: ticket.ConnectionID, Revision: ticket.Revision, RemoteID: ticket.RemoteID, Kind: ticket.Kind, Subtype: ticket.Subtype, Name: ticket.Name, Status: providerresources.Succeeded, IsPreset: true}
	if err = providerresources.ValidatePresetBinding(ctx, b); err != nil {
		s.resourceError(w, err)
		return
	}
	b, err = importer.ImportPreset(ctx, b)
	if err != nil {
		s.resourceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeViewerJSON(w, r, 200, s.resourceView(r, b))
}
