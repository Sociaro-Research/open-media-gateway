package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
)

type unavailableAdmissionService struct {
	serviceStub
	err                        error
	creates, recoveries, hints int
}

func (s *unavailableAdmissionService) CreateMediaRequest(context.Context, core.CreateMediaInput, string) (core.Task, error) {
	s.creates++
	return core.Task{}, s.err
}
func (s *unavailableAdmissionService) RecoverAcceptedIntent(context.Context, string, string) (core.Task, bool, error) {
	s.recoveries++
	return core.Task{}, false, s.err
}
func (s *unavailableAdmissionService) HasAcceptedIntent(context.Context, string) (bool, error) {
	s.hints++
	return false, s.err
}

func assertUnconfirmedAdmission(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var response struct{ Error map[string]any }
	if w.Code != http.StatusServiceUnavailable || json.Unmarshal(w.Body.Bytes(), &response) != nil ||
		len(response.Error) != 4 || response.Error["code"] != "service_unavailable" ||
		response.Error["type"] != "server_error" || response.Error["param"] != nil ||
		!strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatal("admission error contract changed", w.Code, w.Body.String())
	}
	message, _ := response.Error["message"].(string)
	for _, action := range []string{"could not be confirmed", "Journal", "status", "original Idempotency-Key", "endpoint", "request body", "same account and workspace", "operator", "admission and queue"} {
		if !strings.Contains(message, action) {
			t.Fatalf("missing recovery instruction %q: %s", action, message)
		}
	}
	for _, private := range []string{"private-body", "private-token", "private.invalid", "private-key", "foreign-task"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("unverified diagnostic or caller input escaped")
		}
	}
}

func TestGenerationUnconfirmedAdmissionGuidance(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/v1/videos", `{"model":"mock/mock-video-v1","prompt":"private-body"}`},
		{"/v1/generations", `{"model":"mock/mock-video-v1","prompt":"private-body"}`},
		{"/v1/images/generations", `{"model":"mock/mock-image-v1","prompt":"private-body"}`},
	} {
		for _, keyed := range []bool{false, true} {
			t.Run(tc.path+map[bool]string{false: "/admission", true: "/recovery"}[keyed], func(t *testing.T) {
				svc := &unavailableAdmissionService{err: errors.New("private-body https://private.invalid/?token=private-token foreign-task")}
				r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
				r.Header.Set("Authorization", "Bearer secret")
				if keyed {
					r.Header.Set("Idempotency-Key", "private-key")
				}
				w := httptest.NewRecorder()
				New(svc, healthStub{}, "secret").ServeHTTP(w, r)
				assertUnconfirmedAdmission(t, w)
				if keyed && (svc.recoveries != 1 || svc.creates != 0) || !keyed && (svc.creates != 1 || svc.recoveries != 0) {
					t.Fatal("diagnostic changed admission/recovery calls", svc.creates, svc.recoveries)
				}
			})
		}
	}
}

func TestGenerationUnconfirmedMultipartHintDoesNotReadUpload(t *testing.T) {
	svc := &unavailableAdmissionService{err: errors.New("private-body https://private.invalid/?token=private-token foreign-task")}
	store := &directMedia{}
	library := references.New(&referenceMemory{items: map[string]references.Image{}}, store)
	h := NewWithReferenceLibrary(svc, healthStub{}, "secret", store, "signing-secret", time.Hour, nil, nil, library)
	r := httptest.NewRequest("POST", "/v1/images/edits", unreadBody{})
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("Idempotency-Key", "private-key")
	r.Header.Set("Content-Type", "multipart/form-data; boundary=fixture")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertUnconfirmedAdmission(t, w)
	if svc.hints != 1 || svc.recoveries != 0 || svc.creates != 0 {
		t.Fatal("unavailable hint performed recovery or admission", svc.hints, svc.recoveries, svc.creates)
	}
}

func TestGenerationKnownAdmissionRejectionsKeepContract(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{core.ErrPromptRequired, 400, "invalid_request"},
		{core.ErrKeyUnavailable, 401, "unauthorized"},
		{core.ErrBudgetExceeded, 402, "budget_exceeded"},
		{core.ErrIdempotencyConflict, 409, "idempotency_conflict"},
		{core.ErrProviderChanged, 409, "provider_changed"},
		{core.ErrEstimateChanged, 409, "estimate_changed"},
		{core.ErrPricingUnavailable, 422, "pricing_unavailable"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			svc := &unavailableAdmissionService{err: tc.err}
			r := httptest.NewRequest("POST", "/v1/videos", strings.NewReader(`{"model":"mock/mock-video-v1","prompt":"fixture"}`))
			r.Header.Set("Authorization", "Bearer secret")
			w := httptest.NewRecorder()
			New(svc, healthStub{}, "secret").ServeHTTP(w, r)
			var response struct{ Error map[string]any }
			if w.Code != tc.status || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Error["code"] != tc.code || strings.Contains(w.Body.String(), "could not be confirmed") || svc.creates != 1 {
				t.Fatal("known rejection changed", w.Code, w.Body.String(), svc.creates)
			}
		})
	}
}
