package remoteworkspace

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

type Attempt struct {
	Owner        string    `json:"-"`
	ID           string    `json:"id"`
	Origin       string    `json:"origin"`
	Name         string    `json:"client_name"`
	ClientOrigin string    `json:"client_origin"`
	Cipher       []byte    `json:"-"`
	Version      int64     `json:"-"`
	Expires      time.Time `json:"expires_at"`
}
type AuthorizationRepository interface {
	AuthorizationAttempt(context.Context, string) (Attempt, error)
	SaveAuthorizationAttempt(context.Context, Attempt) (Attempt, error)
	ClearAuthorizationAttempt(context.Context, string, string) error
}
type AuthorizationView struct {
	Attempt
	Code            string `json:"user_code,omitempty"`
	VerificationURI string `json:"verification_uri,omitempty"`
	Interval        int    `json:"interval"`
}

func (m *Manager) authorizationRepo() (AuthorizationRepository, error) {
	r, ok := m.Repo.(AuthorizationRepository)
	if !ok {
		return nil, ErrUnavailable
	}
	return r, nil
}
func (m *Manager) Authorization(ctx context.Context, owner string) (AuthorizationView, error) {
	repo, err := m.authorizationRepo()
	if err != nil {
		return AuthorizationView{}, err
	}
	attempt, err := repo.AuthorizationAttempt(ctx, owner)
	if err != nil {
		return AuthorizationView{}, err
	}
	raw, err := m.Cipher.Decrypt(attempt.Cipher)
	if err != nil {
		return AuthorizationView{}, err
	}
	code := wa.UserCode(string(raw))
	return AuthorizationView{Attempt: attempt, Code: code, VerificationURI: attempt.Origin + "/#workspace_authorize=" + code, Interval: 5}, nil
}
func (m *Manager) authorizationRequest(ctx context.Context, origin, path string, value any) (*http.Response, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", origin+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Open-Media-Gateway installation authorization")
	return m.client.Do(request)
}
func (m *Manager) BeginAuthorization(ctx context.Context, owner, origin, name, clientOrigin string, version int64) (AuthorizationView, error) {
	repo, err := m.authorizationRepo()
	if err != nil {
		return AuthorizationView{}, err
	}
	origin, err = Origin(origin)
	if err != nil {
		return AuthorizationView{}, err
	}
	if len(name) < 1 || len(name) > 80 || len(clientOrigin) > 300 {
		return AuthorizationView{}, ErrRoute
	}
	if current, e := m.Repo.RemoteWorkspace(ctx, owner); e == nil && current.Active && current.Provisioning != "dedicated" {
		return AuthorizationView{}, wa.ErrLegacy
	}
	identityProof, err := wa.DeviceCode()
	if err != nil {
		return AuthorizationView{}, err
	}
	identityCipher, err := m.Cipher.Encrypt([]byte(identityProof))
	if err != nil {
		return AuthorizationView{}, err
	}
	identityRepo, ok := m.Repo.(interface {
		InstallationIdentity(context.Context, string, string, []byte) ([]byte, error)
	})
	if !ok {
		return AuthorizationView{}, ErrUnavailable
	}
	identityCipher, err = identityRepo.InstallationIdentity(ctx, owner, origin, identityCipher)
	if err != nil {
		return AuthorizationView{}, err
	}
	identityRaw, err := m.Cipher.Decrypt(identityCipher)
	if err != nil {
		return AuthorizationView{}, err
	}
	code, err := wa.DeviceCode()
	if err != nil {
		return AuthorizationView{}, err
	}
	encrypted, err := m.Cipher.Encrypt([]byte(code))
	if err != nil {
		return AuthorizationView{}, err
	}
	attempt, err := repo.SaveAuthorizationAttempt(ctx, Attempt{Owner: owner, ID: uuid.NewString(), Origin: origin, Name: name, ClientOrigin: clientOrigin, Cipher: encrypted, Version: version, Expires: time.Now().UTC().Add(wa.Lifetime)})
	if err != nil {
		return AuthorizationView{}, err
	}
	if !attempt.Expires.After(time.Now()) {
		return AuthorizationView{}, wa.ErrUnavailable
	}
	raw, err := m.Cipher.Decrypt(attempt.Cipher)
	if err != nil {
		return AuthorizationView{}, err
	}
	replacement := ""
	if current, e := m.Repo.RemoteWorkspace(ctx, owner); e == nil && current.Active && current.Origin == origin {
		replacement = current.KeyID
	}
	response, err := m.authorizationRequest(ctx, origin, "/auth/workspace/start", map[string]any{"device_code": string(raw), "client_name": attempt.Name, "client_origin": attempt.ClientOrigin, "replace_key_id": replacement, "installation_proof": string(identityRaw)})
	if err != nil {
		return AuthorizationView{}, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == 409 {
		var problem struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, MaxJSON)).Decode(&problem) == nil && problem.Error.Code == "installation_upgrade_required" {
			return AuthorizationView{}, wa.ErrLegacy
		}
		return AuthorizationView{}, ErrConflict
	}
	if response.StatusCode == 403 {
		return AuthorizationView{}, wa.ErrDisabled
	}
	if response.StatusCode != 200 {
		return AuthorizationView{}, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxJSON+1))
	if err != nil || len(data) > MaxJSON {
		return AuthorizationView{}, ErrUnavailable
	}
	var reply struct {
		Code string `json:"user_code"`
	}
	if json.Unmarshal(data, &reply) != nil || reply.Code != wa.UserCode(string(raw)) {
		return AuthorizationView{}, ErrUnavailable
	}
	return m.Authorization(ctx, owner)
}
func (m *Manager) PollAuthorization(ctx context.Context, owner, id string) (Record, error) {
	repo, err := m.authorizationRepo()
	if err != nil {
		return Record{}, err
	}
	attempt, err := repo.AuthorizationAttempt(ctx, owner)
	if err != nil {
		return Record{}, err
	}
	if attempt.ID != id {
		return Record{}, ErrConflict
	}
	raw, err := m.Cipher.Decrypt(attempt.Cipher)
	if err != nil {
		return Record{}, err
	}
	response, err := m.authorizationRequest(ctx, attempt.Origin, "/auth/workspace/poll", map[string]string{"device_code": string(raw)})
	if err != nil {
		return Record{}, ErrUnavailable
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case 428:
		return Record{}, wa.ErrPending
	case 429:
		return Record{}, wa.ErrSlow
	case 410:
		return Record{}, wa.ErrUnavailable
	case 403:
		return Record{}, wa.ErrForbidden
	case 200:
	default:
		return Record{}, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxJSON+1))
	if err != nil || len(data) > MaxJSON {
		return Record{}, ErrUnavailable
	}
	var reply struct {
		Token          string `json:"token"`
		Company        string `json:"organization_id"`
		Provisioning   string `json:"provisioning"`
		InstallationID string `json:"installation_id"`
	}
	if json.Unmarshal(data, &reply) != nil {
		return Record{}, ErrUnavailable
	}
	// Never relabel a recovered legacy grant or accept another installation's reply.
	if reply.Provisioning != "dedicated" {
		return Record{}, wa.ErrLegacy
	}
	identityRepo, ok := m.Repo.(interface {
		InstallationIdentity(context.Context, string, string, []byte) ([]byte, error)
	})
	if !ok {
		return Record{}, ErrUnavailable
	}
	identityCipher, err := identityRepo.InstallationIdentity(ctx, owner, attempt.Origin, nil)
	if err != nil {
		return Record{}, err
	}
	identityRaw, err := m.Cipher.Decrypt(identityCipher)
	if err != nil {
		return Record{}, err
	}
	if reply.InstallationID != hex.EncodeToString(wa.Digest("installation:"+string(identityRaw))) {
		return Record{}, ErrConflict
	}
	if current, e := m.Repo.RemoteWorkspace(ctx, owner); e == nil && current.Active && (current.Origin != attempt.Origin || current.OrganizationID != reply.Company || current.Provisioning != "dedicated") {
		return Record{}, ErrConflict
	}
	saved, err := m.connect(ctx, owner, attempt.Origin, reply.Token, reply.Company, attempt.Version, "dedicated")
	if err != nil {
		return Record{}, err
	}
	if err = repo.ClearAuthorizationAttempt(ctx, owner, id); err != nil {
		return Record{}, err
	}
	return saved, nil
}
func (m *Manager) CancelAuthorization(ctx context.Context, owner, id string) error {
	repo, err := m.authorizationRepo()
	if err != nil {
		return err
	}
	attempt, err := repo.AuthorizationAttempt(ctx, owner)
	if err != nil {
		return err
	}
	if attempt.ID != id {
		return ErrConflict
	}
	raw, err := m.Cipher.Decrypt(attempt.Cipher)
	if err != nil {
		return err
	}
	response, err := m.authorizationRequest(ctx, attempt.Origin, "/auth/workspace/cancel", map[string]string{"device_code": string(raw)})
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return ErrUnavailable
	}
	var confirmation struct {
		Cancelled bool `json:"cancelled"`
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, MaxJSON+1))
	if err != nil || len(raw) > MaxJSON || json.Unmarshal(raw, &confirmation) != nil || !confirmation.Cancelled {
		return ErrUnavailable
	}
	return repo.ClearAuthorizationAttempt(ctx, owner, id)
}
