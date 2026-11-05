package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func TestPaymentDiagnosticsRetainEnvelopeAndHideUntrustedText(t *testing.T) {
	const private = "private-sentinel DSN postgres://operator:password@host/account raw-provider-body"
	m := payments.New(nil, nil, "https://gateway.test", nil)
	_, nameErr := m.Save(context.Background(), "actor", "", payments.ProviderUpdate{Kind: "stripe", FeeMode: "service"})
	_, amountErr := payments.Cents(money.USD(money.Scale + 1))
	for _, tc := range []struct {
		err              error
		status           int
		code, field, typ string
	}{
		{fmt.Errorf("%s: %w", private, nameErr), 400, "invalid_payment", "name", "invalid_request_error"},
		{amountErr, 400, "invalid_payment", "amount_usd", "invalid_request_error"},
		{fmt.Errorf("%s: %w", private, payments.ErrInvalid), 400, "invalid_payment", "", "invalid_request_error"},
		{errors.New(private), 503, "service_unavailable", "", "server_error"},
		{fmt.Errorf("%s: %w", private, payments.ErrCallbackOrigin), 503, "service_unavailable", "AUTH_PUBLIC_URL", "server_error"},
		{payments.ErrSignature, 400, "invalid_signature", "", "invalid_request_error"},
		{payments.ErrForbidden, 403, "forbidden", "", "permission_error"},
		{payments.ErrNotFound, 404, "not_found", "", "invalid_request_error"},
	} {
		w := httptest.NewRecorder()
		paymentError(w, tc.err)
		var got struct {
			Error struct {
				Code, Type, Message string
				Param               *string
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != tc.status || got.Error.Code != tc.code || got.Error.Type != tc.typ {
			t.Fatal("envelope changed", w.Code, w.Body.String())
		}
		if tc.field == "" && got.Error.Param != nil || tc.field != "" && (got.Error.Param == nil || *got.Error.Param != tc.field) {
			t.Fatal("wrong field", w.Body.String())
		}
		if strings.Contains(w.Body.String(), private) || strings.Contains(w.Body.String(), "password") {
			t.Fatal("private text escaped")
		}
	}
	for _, field := range []string{"id", "org", "provider_id"} {
		w := httptest.NewRecorder()
		if paymentID(w, private, field) {
			t.Fatal("malformed UUID accepted")
		}
		var got struct {
			Error struct{ Code, Type, Message, Param string }
		}
		json.Unmarshal(w.Body.Bytes(), &got)
		if w.Code != 400 || got.Error.Code != "invalid_payment" || got.Error.Type != "invalid_request_error" || got.Error.Param != field || !strings.Contains(got.Error.Message, "UUID") || strings.Contains(got.Error.Message, private) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestPaymentFieldDiagnosticsFollowExistingAuthorization(t *testing.T) {
	s, _ := newPersonalTestServer()
	orgs := &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}
	orgs.settings.SharedResources = true
	s.system = orgs
	s.payments = payments.New(&paymentAPIFixture{}, nil, "https://gateway.test", nil)
	h := s.handler()
	for _, tc := range []struct {
		session string
		status  int
	}{{"", 401}, {"bob", 403}, {"alice", 400}} {
		w := sendTest(h, "POST", "/v1/system/payment-providers", `{"kind":"stripe","fee_mode":"service","name":""}`, tc.session, "")
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if tc.status != 400 && strings.Contains(w.Body.String(), `"param":"name"`) {
			t.Fatal("unauthorized configuration diagnostic")
		}
	}
	for _, tc := range []struct{ path, field string }{
		{"/v1/system/payment-providers/bad", "id"},
		{"/v1/organizations/bad/payments", "org"},
		{"/v1/organizations/" + fixtureOrg + "/payments/bad", "id"},
	} {
		method := "GET"
		if strings.HasPrefix(tc.path, "/v1/system/") {
			method = "PUT"
		}
		w := sendTest(h, method, tc.path, "", "alice", "")
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"param":"`+tc.field+`"`) {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	w := sendTest(h, "POST", "/v1/organizations/"+fixtureOrg+"/payments", `{"provider_id":"bad","amount_usd":1}`, "alice", "")
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"param":"provider_id"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
