package api

import (
	"context"
	"errors"
	organization "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/google/uuid"
	qrcode "github.com/skip2/go-qrcode"
)

func paymentError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, payments.ErrExpired):
		writeError(w, 410, "payment_expired", e.Error())
	case errors.Is(e, payments.ErrCheckoutWindow):
		writeError(w, 409, "checkout_window", e.Error())
	case errors.Is(e, payments.ErrVATSettings):
		writeError(w, 400, "vat_settings_required", e.Error())
	case errors.Is(e, payments.ErrSellerVATNumber):
		writeError(w, 400, "invalid_seller_vat_number", e.Error())
	case errors.Is(e, payments.ErrVATRule):
		writeError(w, 400, "vat_rule_missing", e.Error())
	case errors.Is(e, payments.ErrVATVerification):
		writeError(w, 400, "vat_verification_required", e.Error())
	case errors.Is(e, payments.ErrVATUnavailable):
		writeError(w, 503, "vat_verification_unavailable", e.Error())
	case errors.Is(e, payments.ErrAmountExhausted):
		writeError(w, 409, "payment_amount_exhausted", "No unused payment amounts remain. Choose another top-up amount.")
	case errors.Is(e, payments.ErrInvalid):
		writeError(w, 400, "invalid_payment", "Use USD with whole cents, between $1 and $10,000, and the documented payment fields.")
	case errors.Is(e, payments.ErrForbidden):
		writeError(w, 403, "forbidden", "This payment operation is not permitted.")
	case errors.Is(e, payments.ErrSignature):
		writeError(w, 400, "invalid_signature", "Invalid webhook signature.")
	case errors.Is(e, payments.ErrConflict):
		writeError(w, 409, "payment_conflict", "The operation conflicts with an existing payment or configuration. Refresh settings; reuse an idempotency key only with the same payment details.")
	case errors.Is(e, payments.ErrNotFound):
		writeError(w, 404, "not_found", "Payment or provider not found.")
	case errors.Is(e, payments.ErrUnavailable):
		writeError(w, 503, "payment_unavailable", "The payment service is temporarily unavailable. Please try again.")
	default:
		orgError(w, e)
	}
}
func paymentID(w http.ResponseWriter, id string) bool {
	if _, e := uuid.Parse(id); e != nil {
		paymentError(w, payments.ErrInvalid)
		return false
	}
	return true
}
func (s *Server) registerPayments(mux *http.ServeMux) {
	mux.Handle("GET /v1/system/billing", s.authenticate(http.HandlerFunc(s.paymentVATSettings)))
	mux.Handle("PUT /v1/system/billing", s.authenticate(http.HandlerFunc(s.paymentVATSettings)))
	mux.Handle("GET /v1/organizations/{org}/vat-verification", s.authenticate(http.HandlerFunc(s.organizationVATVerification)))
	mux.Handle("PUT /v1/organizations/{org}/vat-verification", s.authenticate(http.HandlerFunc(s.organizationVATVerification)))
	mux.Handle("GET /v1/system/payment-notifications", s.authenticate(http.HandlerFunc(s.paymentNotificationSettings)))
	mux.Handle("PUT /v1/system/payment-notifications", s.authenticate(http.HandlerFunc(s.paymentNotificationSettings)))
	mux.Handle("GET /v1/system/payment-providers", s.authenticate(http.HandlerFunc(s.paymentProviders)))
	mux.Handle("POST /v1/system/payment-providers", s.authenticate(http.HandlerFunc(s.paymentProviders)))
	mux.Handle("PUT /v1/system/payment-providers/{id}", s.authenticate(http.HandlerFunc(s.paymentProviders)))
	mux.Handle("POST /v1/system/payment-providers/{id}/check", s.authenticate(http.HandlerFunc(s.paymentProviders)))
	mux.Handle("POST /v1/system/payment-providers/{id}/disable", s.authenticate(http.HandlerFunc(s.paymentProviders)))
	mux.Handle("POST /v1/system/payment-integrations", s.authenticate(http.HandlerFunc(s.paymentProviders)))
	mux.Handle("GET /v1/organizations/{org}/payment-methods", s.authenticate(http.HandlerFunc(s.organizationPayments)))
	mux.Handle("POST /v1/organizations/{org}/payments", s.authenticate(http.HandlerFunc(s.organizationPayments)))
	mux.Handle("GET /v1/organizations/{org}/payments", s.authenticate(http.HandlerFunc(s.organizationPayments)))
	mux.Handle("GET /v1/organizations/{org}/payments/{id}/invoice", s.authenticate(http.HandlerFunc(s.organizationPayments)))
	mux.Handle("GET /v1/organizations/{org}/payments/{id}/qr", s.authenticate(http.HandlerFunc(s.organizationPayments)))
	mux.Handle("GET /v1/organizations/{org}/payments/{id}", s.authenticate(http.HandlerFunc(s.organizationPayments)))
	mux.Handle("DELETE /v1/organizations/{org}/payments/{id}", s.authenticate(http.HandlerFunc(s.organizationPayments)))
	mux.Handle("POST /v1/organizations/{org}/payments/{id}/checkout", s.authenticate(http.HandlerFunc(s.organizationPayments)))
	mux.Handle("POST /v1/organizations/{org}/payments/{id}/refresh", s.authenticate(http.HandlerFunc(s.organizationPayments)))
	mux.HandleFunc("POST /v1/payments/webhooks/{id}", s.paymentWebhook)
	mux.HandleFunc("POST /v1/payment-integrations/{id}/settlements", s.externalSettlement)
}
func (s *Server) paymentsAvailable(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if s.payments == nil {
		paymentError(w, payments.ErrUnavailable)
		return false
	}
	return true
}
func (s *Server) paymentProviders(w http.ResponseWriter, r *http.Request) {
	if !s.paymentsAvailable(w) {
		return
	}
	admin, e := s.systemAdmin(r.Context())
	if e != nil {
		paymentError(w, e)
		return
	}
	if !admin {
		paymentError(w, payments.ErrForbidden)
		return
	}
	id := r.PathValue("id")
	if id != "" && !paymentID(w, id) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	switch {
	case readMethod(r.Method):
		ps, e := s.payments.Providers(ctx)
		if e != nil {
			paymentError(w, e)
			return
		}
		s.writeViewerJSON(w, r, 200, map[string]any{"data": ps, "available_providers": []map[string]string{{"id": "stripe", "name": "Stripe"}, {"id": "stablecoin", "name": "Stablecoins"}}, "stablecoin_networks": payments.StablecoinNetworks()})
		return
	case strings.HasSuffix(r.URL.Path, "/check"):
		a, e := s.payments.Check(ctx, id)
		if e != nil {
			paymentError(w, e)
			return
		}
		s.writeViewerJSON(w, r, 200, map[string]any{"account_id": a.ID, "live": a.Live, "charges_enabled": a.ChargesEnabled})
		return
	case strings.HasSuffix(r.URL.Path, "/disable"):
		var in struct {
			Version int64 `json:"version"`
		}
		if !decodeSystem(w, r, &in) {
			return
		}
		p, e := s.payments.Disable(ctx, identity.ActorID(ctx), id, in.Version)
		if e != nil {
			paymentError(w, e)
			return
		}
		s.writeViewerJSON(w, r, 200, p)
		return
	case strings.HasSuffix(r.URL.Path, "/payment-integrations"):
		var in struct {
			Name string `json:"name"`
			Live bool   `json:"live"`
		}
		if !decodeSystem(w, r, &in) {
			return
		}
		p, token, e := s.payments.NewIntegration(ctx, identity.ActorID(ctx), in.Name, in.Live)
		if e != nil {
			paymentError(w, e)
			return
		}
		s.writeViewerJSON(w, r, 201, map[string]any{"provider": p, "token": token})
		return
	default:
		var in payments.ProviderUpdate
		if !decodeSystem(w, r, &in) {
			return
		}
		p, e := s.payments.Save(ctx, identity.ActorID(ctx), id, in)
		if e != nil {
			paymentError(w, e)
			return
		}
		code := 200
		if r.Method == "POST" {
			code = 201
		}
		s.writeViewerJSON(w, r, code, p)
	}
}
func (s *Server) organizationPayments(w http.ResponseWriter, r *http.Request) {
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
	repo := s.organizationStore()
	if repo == nil {
		paymentError(w, payments.ErrUnavailable)
		return
	}
	actor := identity.ActorID(r.Context())
	role, e := repo.OrganizationAccess(r.Context(), actor, org)
	if e != nil {
		paymentError(w, e)
		return
	}
	if !organization.Manage(role) {
		paymentError(w, payments.ErrForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if e = s.payments.ExpirePayments(ctx); e != nil {
		paymentError(w, e)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/payment-methods") {
		ps, e := s.payments.Providers(ctx)
		if e != nil {
			paymentError(w, e)
			return
		}
		methods := []map[string]any{}
		for _, p := range ps {
			if (p.Kind == "stripe" || p.Kind == "stablecoin") && p.Enabled && (p.Live || role == "system_admin") {
				methods = append(methods, map[string]any{"id": p.ID, "name": p.Name, "kind": p.Kind, "live": p.Live, "fee_mode": p.FeeMode, "stablecoin": p.Stablecoin, "stripe_invoices": p.StripeInvoices})
			}
		}
		s.writeViewerJSON(w, r, 200, map[string]any{"data": methods, "currency": "usd", "min_cents": payments.MinCents, "max_cents": payments.MaxCents})
		return
	}
	id := r.PathValue("id")
	if id != "" {
		if !paymentID(w, id) {
			return
		}
		p, e := s.payments.Repo.Payment(ctx, id)
		if e != nil {
			paymentError(w, e)
			return
		}
		if p.OrganizationID != org || !p.Live && role != "system_admin" {
			paymentError(w, payments.ErrForbidden)
			return
		}
		if r.Method == "DELETE" {
			stable, ok := s.payments.Repo.(payments.StablecoinRepository)
			if !ok {
				paymentError(w, payments.ErrUnavailable)
				return
			}
			p, e = stable.CancelStablecoin(ctx, actor, org, id)
			if e != nil {
				paymentError(w, e)
				return
			}
		}
		if r.Method == "POST" {
			if strings.HasSuffix(r.URL.Path, "/checkout") {
				p, e = s.payments.ResumeInvoiceCheckout(ctx, id)
			} else {
				p, e = s.payments.Reconcile(ctx, id)
			}
			if e != nil {
				paymentError(w, e)
				return
			}
		}
		if strings.HasSuffix(r.URL.Path, "/invoice") {
			doc, err := s.payments.InvoicePDF(ctx, id)
			if err != nil {
				paymentError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", `attachment; filename="`+payments.InvoiceFileName(doc.Number)+`"`)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Write(doc.PDF)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/qr") {
			if p.Stablecoin == nil {
				paymentError(w, payments.ErrNotFound)
				return
			}
			png, err := qrcode.Encode(p.Stablecoin.Address, qrcode.Medium, 256)
			if err != nil {
				paymentError(w, payments.ErrUnavailable)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Write(png)
			return
		}
		s.writeViewerJSON(w, r, 200, p)
		return
	}
	if readMethod(r.Method) {
		offset, ok := systemPage(w, r)
		if !ok {
			return
		}
		ps, e := s.payments.Repo.Payments(ctx, org, offset)
		if e != nil {
			paymentError(w, e)
			return
		}
		more := len(ps) > 50
		if more {
			ps = ps[:50]
		}
		visible := []payments.Payment{}
		for _, p := range ps {
			if p.Live || role == "system_admin" {
				visible = append(visible, p)
			}
		}
		s.writeViewerJSON(w, r, 200, map[string]any{"data": visible, "has_more": more})
		return
	}
	var in struct {
		ProviderID string                  `json:"provider_id"`
		Amount     money.USD               `json:"amount_usd"`
		FeeMode    string                  `json:"fee_mode"`
		Network    string                  `json:"network"`
		Token      string                  `json:"token"`
		Billing    payments.BillingProfile `json:"billing"`
	}
	if !decodeSystem(w, r, &in) || !paymentID(w, in.ProviderID) {
		return
	}
	var p payments.Payment
	if in.Network != "" || in.Token != "" {
		p, e = s.payments.StablecoinCheckout(ctx, actor, org, payments.StablecoinInput{ProviderID: in.ProviderID, Amount: in.Amount, Network: in.Network, Token: in.Token, Billing: in.Billing}, r.Header.Get("Idempotency-Key"))
	} else {
		p, e = s.payments.CheckoutBilling(ctx, actor, org, in.ProviderID, in.Amount, r.Header.Get("Idempotency-Key"), in.FeeMode, in.Billing)
	}
	if e != nil {
		paymentError(w, e)
		return
	}
	s.writeViewerJSON(w, r, 201, p)
}
func (s *Server) paymentWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.paymentsAvailable(w) || !paymentID(w, r.PathValue("id")) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	defer r.Body.Close()
	body, e := io.ReadAll(r.Body)
	if e != nil {
		writeError(w, 413, "request_too_large", "Webhook body exceeds the limit.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if e = s.payments.Webhook(ctx, r.PathValue("id"), body, r.Header.Get("Stripe-Signature")); e != nil {
		paymentError(w, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) externalSettlement(w http.ResponseWriter, r *http.Request) {
	if !s.paymentsAvailable(w) || !paymentID(w, r.PathValue("id")) {
		return
	}
	// Separate trust domain: browser sessions, gateway keys, and Stripe secrets
	// are never accepted as settlement authority.
	headers := r.Header.Values("Authorization")
	if len(headers) != 1 || r.Header.Get("X-API-Key") != "" {
		paymentError(w, payments.ErrForbidden)
		return
	}
	parts := strings.Fields(headers[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !strings.HasPrefix(parts[1], "pay_") || len(parts[1]) != 47 {
		paymentError(w, payments.ErrForbidden)
		return
	}
	var in payments.ExternalInput
	if !decodeSystem(w, r, &in) {
		return
	}
	p, e := s.payments.ExternalSettlement(r.Context(), r.PathValue("id"), payments.HashToken(parts[1]), in)
	if e != nil {
		paymentError(w, e)
		return
	}
	s.writeViewerJSON(w, r, 200, p)
}
