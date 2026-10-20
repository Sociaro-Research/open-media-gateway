package api

import (
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	organization "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
)

func (s *Server) paymentVATSettings(w http.ResponseWriter, r *http.Request) {
	if !s.paymentsAvailable(w) {
		return
	}
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		paymentError(w, err)
		return
	}
	if !admin {
		paymentError(w, payments.ErrForbidden)
		return
	}
	repo, ok := s.payments.Repo.(payments.VATRepository)
	if !ok {
		paymentError(w, payments.ErrUnavailable)
		return
	}
	var settings payments.VATSettings
	if r.Method == "PUT" {
		if !decodeSystemLimit(w, r, &settings, payments.MaxBillingRequestBytes) {
			return
		}
		settings, err = repo.SaveVATSettings(r.Context(), identity.ActorID(r.Context()), settings)
	} else {
		settings, err = repo.VATSettings(r.Context())
	}
	if err != nil {
		paymentError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, settings)
}
func (s *Server) organizationVATVerification(w http.ResponseWriter, r *http.Request) {
	if !s.paymentsAvailable(w) {
		return
	}
	org := r.PathValue("org")
	if !paymentID(w, org) {
		return
	}
	if bound := credentialOrganization(r.Context()); bound != "" && bound != org {
		paymentError(w, payments.ErrForbidden)
		return
	}
	store := s.organizationStore()
	if store == nil {
		paymentError(w, payments.ErrUnavailable)
		return
	}
	role, err := store.OrganizationAccess(r.Context(), identity.ActorID(r.Context()), org)
	if err != nil {
		paymentError(w, err)
		return
	}
	if !organization.Manage(role) {
		paymentError(w, payments.ErrForbidden)
		return
	}
	repo, ok := s.payments.Repo.(payments.VATRepository)
	if !ok {
		paymentError(w, payments.ErrUnavailable)
		return
	}
	var v payments.VATVerification
	if r.Method == "PUT" {
		admin, e := s.systemAdmin(r.Context())
		if e != nil {
			paymentError(w, e)
			return
		}
		if !admin {
			paymentError(w, payments.ErrForbidden)
			return
		}
		if !decodeSystem(w, r, &v) {
			return
		}
		v, err = repo.SaveVATVerification(r.Context(), identity.ActorID(r.Context()), org, v)
	} else {
		v, err = repo.VATVerification(r.Context(), org)
	}
	if err != nil {
		paymentError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, v)
}
