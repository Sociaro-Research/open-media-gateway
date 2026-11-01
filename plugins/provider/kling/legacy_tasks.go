package kling

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

var decimalInteger = regexp.MustCompile(`^[0-9]+$`)

// IDs remain opaque decimal/string tokens; never decode through float64.
type legacyOpaqueID string

func (id *legacyOpaqueID) UnmarshalJSON(raw []byte) error {
	var value string
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
	} else {
		value = string(raw)
		if !decimalInteger.MatchString(value) {
			return errors.New("invalid opaque ID")
		}
	}
	if !validTaskID(value) {
		return errors.New("invalid opaque ID")
	}
	*id = legacyOpaqueID(value)
	return nil
}

func legacyScope(ctx context.Context) (string, error) {
	scope, ok := core.ProviderConnectionScopeFromContext(ctx)
	if !ok {
		return "", errors.Join(core.ErrOptionsInvalid, errors.New("Kling operation requires a host-authorized concrete connection scope"))
	}
	raw, _ := json.Marshal([]string{scope.ID, scope.OwnerID, scope.Revision, identity.OwnerID(ctx)})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type legacyTaskHandle struct {
	Operation string `json:"operation"`
	ID        string `json:"id"`
	Scope     string `json:"scope"`
}

const legacyTaskPrefix = "kling-v1."

func (a *Adapter) signLegacy(ctx context.Context, operation, id string) (string, error) {
	scope, err := legacyScope(ctx)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(legacyTaskHandle{Operation: operation, ID: id, Scope: scope})
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(a.apiKey))
	mac.Write([]byte("kling-task-v1\n" + encoded))
	return legacyTaskPrefix + encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (a *Adapter) parseLegacy(ctx context.Context, handle string) (legacyTaskHandle, error) {
	invalid := errors.New("invalid Kling task handle for this owner, connection or revision")
	if len(handle) > 4096 || !strings.HasPrefix(handle, legacyTaskPrefix) {
		return legacyTaskHandle{}, invalid
	}
	encoded, signature, ok := strings.Cut(strings.TrimPrefix(handle, legacyTaskPrefix), ".")
	if !ok {
		return legacyTaskHandle{}, invalid
	}
	supplied, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return legacyTaskHandle{}, invalid
	}
	mac := hmac.New(sha256.New, []byte(a.apiKey))
	mac.Write([]byte("kling-task-v1\n" + encoded))
	if !hmac.Equal(mac.Sum(nil), supplied) {
		return legacyTaskHandle{}, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return legacyTaskHandle{}, invalid
	}
	var h legacyTaskHandle
	if json.Unmarshal(raw, &h) != nil || !validTaskID(h.ID) || h.Operation == "" {
		return legacyTaskHandle{}, invalid
	}
	scope, err := legacyScope(ctx)
	if err != nil || h.Scope != scope {
		return legacyTaskHandle{}, invalid
	}
	return h, nil
}
func (a *Adapter) submitLegacy(ctx context.Context, operation, path string, payload any) (core.ProviderTask, error) {
	if _, err := legacyScope(ctx); err != nil {
		return core.ProviderTask{}, err
	}
	raw, err := a.do(ctx, http.MethodPost, path, payload)
	if err != nil {
		return core.ProviderTask{}, err
	}
	var data struct {
		ID     legacyOpaqueID `json:"task_id"`
		Status string         `json:"task_status"`
	}
	if json.Unmarshal(raw, &data) != nil || data.ID == "" {
		return core.ProviderTask{}, errors.New("Kling legacy submission has no valid task ID; check provider history before resubmitting")
	}
	switch data.Status {
	case "submitted", "processing", "succeed":
	case "failed":
		return core.ProviderTask{}, errors.New("Kling generation failed during submission; check provider task details")
	default:
		return core.ProviderTask{}, errors.New("Kling legacy submission returned an unknown task status")
	}
	handle, err := a.signLegacy(ctx, operation, string(data.ID))
	return core.ProviderTask{ID: handle}, err
}
