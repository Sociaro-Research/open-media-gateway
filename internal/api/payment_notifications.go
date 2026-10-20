package api

import (
	"errors"
	"net/http"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
)

func (s *Server) paymentNotificationSettings(w http.ResponseWriter, r *http.Request) {
	if !s.paymentsAvailable(w) {
		return
	}
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin {
		paymentError(w, payments.ErrForbidden)
		return
	}
	repo, ok := s.payments.Repo.(payments.NotificationRepository)
	if !ok {
		paymentError(w, payments.ErrUnavailable)
		return
	}
	var settings payments.NotificationSettings
	if r.Method == http.MethodPut {
		if !decodeSystem(w, r, &settings) {
			return
		}
		settings, err = repo.SavePaymentNotificationSettings(r.Context(), identity.ActorID(r.Context()), settings)
	} else {
		settings, err = repo.PaymentNotificationSettings(r.Context())
	}
	if errors.Is(err, payments.ErrNotificationSettings) {
		writeError(w, 400, "invalid_notification_settings", err.Error())
		return
	}
	if err != nil {
		systemError(w, err)
		return
	}
	health, err := repo.PaymentNotificationHealth(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	mailEnabled := false
	if s.emailDelivery != nil {
		mailEnabled, err = s.emailDelivery.Configured(r.Context())
		if err != nil {
			systemError(w, err)
			return
		}
	}
	s.writeViewerJSON(w, r, 200, struct {
		payments.NotificationSettings
		EmailEnabled bool                        `json:"email_enabled"`
		Delivery     payments.NotificationHealth `json:"delivery"`
	}{settings, mailEnabled, health})
}
