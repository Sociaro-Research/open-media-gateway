package api

import (
	"context"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"net/http"
)

func (s *Server) companyPaymentStatus(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.companyInterface.(interface {
		CompanyPaymentStatus(context.Context, string) (payments.Payment, error)
	})
	if !ok {
		companyInterfaceError(w, ci.ErrProtocol)
		return
	}
	p, err := repo.CompanyPaymentStatus(r.Context(), r.PathValue("id"))
	if err != nil {
		companyInterfaceError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, p)
}
