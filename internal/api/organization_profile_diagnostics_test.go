package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
)

type diagnosticProfileStore struct {
	auth.Repository
	writes int
}

func (f *diagnosticProfileStore) UpdateProfile(_ context.Context, id string, in auth.ProfileUpdate) (identity.User, error) {
	f.writes++
	return identity.User{ID: id}, nil
}

type diagnosticProfileAuth struct {
	browserAuthStub
	manager *auth.Manager
	err     error
}

func (f *diagnosticProfileAuth) UpdateProfile(ctx context.Context, id string, in auth.ProfileUpdate) (identity.User, error) {
	if f.err != nil {
		return identity.User{}, f.err
	}
	return f.manager.UpdateProfile(ctx, id, in)
}

func TestProfileHTTPFieldDiagnosticsAndSafeFallback(t *testing.T) {
	s, _ := newPersonalTestServer()
	store := &diagnosticProfileStore{}
	manager, err := auth.New(context.Background(), store, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "https://gateway.test"})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &diagnosticProfileAuth{manager: manager}
	s.auth = fixture
	h := s.handler()
	for _, tc := range []struct{ body, field string }{
		{`{}`, "body"}, {`{"name":" "}`, "name"}, {`{"theme":"PRIVATE_SENTINEL"}`, "theme"}, {`{"color_preset":"PRIVATE_SENTINEL"}`, "color_preset"},
		{`{"report_columns":["date","PRIVATE_SENTINEL"]}`, "report_columns[1]"},
		{`{"contacts":[{"label":"Fine","url":"https://example.test"},{"label":"Fine","url":"javascript:PRIVATE_SENTINEL"}]}`, "contacts[1].url"},
	} {
		w := sendTest(h, "PATCH", "/v1/me", tc.body, "alice", "")
		var got struct {
			Error struct{ Code, Type, Message, Param string }
		}
		json.Unmarshal(w.Body.Bytes(), &got)
		if w.Code != 400 || got.Error.Code != "invalid_profile" || got.Error.Type != "invalid_request_error" || got.Error.Param != tc.field || strings.Contains(w.Body.String(), "PRIVATE_SENTINEL") {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	fixture.err = fmt.Errorf("PRIVATE_SENTINEL: %w", auth.ErrInvalidProfile)
	w := sendTest(h, "PATCH", "/v1/me", `{"name":"Fine"}`, "alice", "")
	if w.Code != 400 || strings.Contains(w.Body.String(), "PRIVATE_SENTINEL") {
		t.Fatal(w.Code, w.Body.String())
	}
	if store.writes != 0 {
		t.Fatal("rejected profile wrote data")
	}
	// API key/own-browser coverage is also retained by TestProfileUpdatesRequireOwnBrowserSession.
	if w := sendTest(h, "PATCH", "/v1/me", `{"theme":"PRIVATE_SENTINEL"}`, "", ""); w.Code != 401 || strings.Contains(w.Body.String(), `"param":"theme"`) {
		t.Fatal(w.Code, w.Body.String())
	}

}

func TestOrganizationHTTPDiagnosticsOnlyUseKnownCauses(t *testing.T) {
	for _, tc := range []struct {
		err   error
		field string
	}{
		{org.InvalidName, "name"}, {org.InvalidCreditLimit, "credit_limit_usd"}, {org.InvalidDefaultWeeklyLimit, "default_weekly_limit_usd"},
		{org.InvalidCreditReason, "reason"}, {org.InvalidCreditExternalID, "external_id"}, {org.ValidationError(255), ""},
	} {
		w := httptest.NewRecorder()
		orgError(w, fmt.Errorf("PRIVATE_SENTINEL postgres://operator:password@host: %w", tc.err))
		var got struct {
			Error struct {
				Code, Type, Message string
				Param               *string
			}
		}
		json.Unmarshal(w.Body.Bytes(), &got)
		if w.Code != 400 || got.Error.Code != "invalid_request" || got.Error.Type != "invalid_request_error" || strings.Contains(w.Body.String(), "PRIVATE_SENTINEL") || strings.Contains(w.Body.String(), "password") {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.field == "" && got.Error.Param != nil || tc.field != "" && (got.Error.Param == nil || *got.Error.Param != tc.field) {
			t.Fatal("wrong field", w.Body.String())
		}
	}
	// Exercise the actual repository preflight through the public error mapper.
	repo := postgres.NewSystemRepository(nil)
	for _, name := range []string{" ", strings.Repeat("é", 81)} {
		_, err := repo.CreateOrganization(context.Background(), "actor", name, "", "")
		w := httptest.NewRecorder()
		orgError(w, err)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"param":"name"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
