package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/audit"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
	"github.com/Sociaro-Research/open-media-gateway/internal/providers"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

type Service interface {
	CreateMediaRequest(context.Context, core.CreateMediaInput, string) (core.Task, error)
	GetTask(context.Context, string) (core.Task, error)
	ListTasks(context.Context, int) ([]core.Task, error)
	ListProviders(context.Context) []core.ProviderDescriptor
	Info() core.ServiceInfo
}

type HealthChecker interface {
	Check(context.Context) error
}

type ProviderAdministration interface {
	List(context.Context) ([]providers.AdminProvider, error)
	Get(context.Context, string) (providers.AdminProvider, error)
	Update(context.Context, string, providers.ProviderUpdate) (providers.AdminProvider, error)
	Check(context.Context, string) (providers.AdminProvider, error)
}

type BrowserAuth interface {
	Authenticate(*http.Request) (identity.User, error)
	Login(http.ResponseWriter, *http.Request)
	Callback(http.ResponseWriter, *http.Request)
	Logout(http.ResponseWriter, *http.Request)
}

type Server struct {
	providerAssets *providerassets.Service
	assetPublicURL string
	catalogSync    catalogsync.Repository
	modelAccess    modelaccess.Store
	payments       *payments.Manager
	system         system.Repository
	service        Service
	health         HealthChecker
	apiKey         string
	storage        storage.Store
	storageAdmin   StorageAdministration
	mediaSigner    *mediaURLSigner
	providerAdmin  ProviderAdministration
	loginProviders *auth.LoginProviderManager
	emailDelivery  *auth.DeliveryManager
	onboarding     *organization.Onboarding
	auth           BrowserAuth
	keys           apikey.Repository
	references     *references.Library
}

func New(service Service, health HealthChecker, apiKey string) http.Handler {
	return NewWithStorage(service, health, apiKey, nil, "", time.Hour)
}

func NewWithStorage(service Service, health HealthChecker, apiKey string, mediaStorage storage.Store, signingSecret string, mediaURLTTL time.Duration) http.Handler {
	return NewWithProviderAdmin(service, health, apiKey, mediaStorage, signingSecret, mediaURLTTL, nil)
}

func NewWithProviderAdmin(service Service, health HealthChecker, apiKey string, mediaStorage storage.Store, signingSecret string, mediaURLTTL time.Duration, providerAdmin ProviderAdministration) http.Handler {
	return NewWithGoogleAuth(service, health, apiKey, mediaStorage, signingSecret, mediaURLTTL, providerAdmin, nil)
}

func NewWithGoogleAuth(service Service, health HealthChecker, apiKey string, mediaStorage storage.Store, signingSecret string, mediaURLTTL time.Duration, providerAdmin ProviderAdministration, authManager *auth.Manager, keyStores ...apikey.Repository) http.Handler {
	return NewWithReferenceLibrary(service, health, apiKey, mediaStorage, signingSecret, mediaURLTTL, providerAdmin, authManager, nil, keyStores...)
}

func NewWithReferenceLibrary(service Service, health HealthChecker, apiKey string, mediaStorage storage.Store, signingSecret string, mediaURLTTL time.Duration, providerAdmin ProviderAdministration, authManager *auth.Manager, library *references.Library, keyStores ...apikey.Repository) http.Handler {
	return NewWithSystemAdministration(service, health, apiKey, mediaStorage, signingSecret, mediaURLTTL, providerAdmin, authManager, library, nil, keyStores...)
}
func NewWithSystemAdministration(service Service, health HealthChecker, apiKey string, mediaStorage storage.Store, signingSecret string, mediaURLTTL time.Duration, providerAdmin ProviderAdministration, authManager *auth.Manager, library *references.Library, sys system.Repository, keyStores ...apikey.Repository) http.Handler {
	return NewWithPayments(service, health, apiKey, mediaStorage, signingSecret, mediaURLTTL, providerAdmin, authManager, library, sys, nil, keyStores...)
}
func NewWithPayments(service Service, health HealthChecker, apiKey string, mediaStorage storage.Store, signingSecret string, mediaURLTTL time.Duration, providerAdmin ProviderAdministration, authManager *auth.Manager, library *references.Library, sys system.Repository, funding *payments.Manager, keyStores ...apikey.Repository) http.Handler {
	var keys apikey.Repository
	if len(keyStores) > 0 {
		keys = keyStores[0]
	}
	return NewGateway(Dependencies{Service: service, Health: health, APIKey: apiKey, Storage: mediaStorage, SigningSecret: signingSecret, MediaURLTTL: mediaURLTTL, ProviderAdmin: providerAdmin, Auth: authManager, References: library, System: sys, Payments: funding, Keys: keys})
}

// Dependencies keeps management services independent of browser authentication.
type Dependencies struct {
	ProviderAssets *providerassets.Service
	AssetPublicURL string
	CatalogSync    catalogsync.Repository
	Service        Service
	Health         HealthChecker
	APIKey         string
	Storage        storage.Store
	SigningSecret  string
	MediaURLTTL    time.Duration
	ProviderAdmin  ProviderAdministration
	Auth           *auth.Manager
	References     *references.Library
	System         system.Repository
	Payments       *payments.Manager
	Keys           apikey.Repository
	LoginProviders *auth.LoginProviderManager
	EmailDelivery  *auth.DeliveryManager
	Onboarding     *organization.Onboarding
}

func NewGateway(d Dependencies) http.Handler {
	service, health, apiKey, mediaStorage, signingSecret, mediaURLTTL := d.Service, d.Health, d.APIKey, d.Storage, d.SigningSecret, d.MediaURLTTL
	providerAdmin, authManager, library, sys, funding := d.ProviderAdmin, d.Auth, d.References, d.System, d.Payments
	s := &Server{service: service, health: health, apiKey: apiKey, storage: mediaStorage, providerAdmin: providerAdmin, payments: funding}
	s.references = library
	s.providerAssets = d.ProviderAssets
	s.assetPublicURL = strings.TrimRight(d.AssetPublicURL, "/")
	s.system = sys
	s.catalogSync = d.CatalogSync
	s.modelAccess, _ = sys.(modelaccess.Store)
	if authManager != nil {
		s.auth = authManager
		s.emailDelivery = authManager.EmailDelivery()
		s.loginProviders = authManager.LoginProviders()
	}
	s.keys = d.Keys
	s.onboarding = d.Onboarding
	if d.LoginProviders != nil {
		s.loginProviders = d.LoginProviders
	}
	if d.EmailDelivery != nil {
		s.emailDelivery = d.EmailDelivery
	}
	if admin, ok := mediaStorage.(StorageAdministration); ok {
		s.storageAdmin = admin
	}
	if mediaStorage != nil {
		s.mediaSigner = newMediaURLSigner(signingSecret, mediaURLTTL)
	}
	return s.handler()
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /site.json", s.siteConfiguration)
	mux.HandleFunc("GET /legal/{kind}", s.legalDocument)
	mux.HandleFunc("GET /auth/session", s.browserSession)
	mux.HandleFunc("GET /auth/monitoring", s.monitoringSession)
	mux.HandleFunc("GET /branding/assets/{asset}", func(w http.ResponseWriter, r *http.Request) { brandingAsset(w, r, s.brandingReader()) })
	s.registerOrganizations(mux)
	s.registerAudit(mux)
	mux.Handle("GET /v1/system/model-settings", s.authenticate(http.HandlerFunc(s.modelSettings)))
	mux.Handle("PUT /v1/system/model-settings/{model}", s.authenticate(http.HandlerFunc(s.modelSettings)))
	mux.Handle("GET /v1/organizations/{org}/model-access", s.authenticate(http.HandlerFunc(s.modelSettings)))
	mux.Handle("PUT /v1/organizations/{org}/model-access", s.authenticate(http.HandlerFunc(s.modelSettings)))
	s.registerPayments(mux)
	mux.Handle("GET /v1/storage/usage", s.authenticate(http.HandlerFunc(s.storageUsage)))
	if s.system != nil {
		mux.Handle("GET /v1/system/legal/{kind}", s.authenticate(http.HandlerFunc(s.legalSettings)))
		mux.Handle("PUT /v1/system/legal/{kind}", s.authenticate(http.HandlerFunc(s.legalSettings)))
		mux.Handle("GET /v1/system/branding", s.authenticate(http.HandlerFunc(s.brandingSettings)))
		mux.Handle("PUT /v1/system/branding", s.authenticate(http.HandlerFunc(s.brandingSettings)))
		mux.Handle("GET /v1/system/storage/quota", s.authenticate(http.HandlerFunc(s.storageQuota)))
		mux.Handle("PUT /v1/system/storage/quota", s.authenticate(http.HandlerFunc(s.storageQuota)))
		mux.Handle("POST /v1/system/resources/import", s.authenticate(http.HandlerFunc(s.importShared)))
		mux.Handle("GET /v1/system/reports/{id}", s.authenticate(http.HandlerFunc(s.reportDetail)))
		mux.Handle("GET /v1/system/reports/{id}/content", s.authenticate(http.HandlerFunc(s.reportArtifact)))
		mux.Handle("HEAD /v1/system/reports/{id}/content", s.authenticate(http.HandlerFunc(s.reportArtifact)))
		mux.Handle("GET /v1/system/reports/{id}/last-frame", s.authenticate(http.HandlerFunc(s.reportArtifact)))
		mux.Handle("HEAD /v1/system/reports/{id}/last-frame", s.authenticate(http.HandlerFunc(s.reportArtifact)))
		mux.Handle("GET /v1/system/reports", s.authenticate(http.HandlerFunc(s.costReport)))
		mux.Handle("GET /v1/system/providers/types", s.authenticate(http.HandlerFunc(s.providerTypes)))
		mux.Handle("POST /v1/system/providers", s.authenticate(http.HandlerFunc(s.createAdminProvider)))
		mux.Handle("GET /v1/system/providers", s.authenticate(http.HandlerFunc(s.listAdminProviders)))
		mux.Handle("GET /v1/system/providers/{id}", s.authenticate(http.HandlerFunc(s.getAdminProvider)))
		mux.Handle("PUT /v1/system/providers/{id}", s.authenticate(http.HandlerFunc(s.updateAdminProvider)))
		mux.Handle("POST /v1/system/providers/{id}/check", s.authenticate(http.HandlerFunc(s.checkAdminProvider)))
		mux.Handle("GET /v1/system/storage", s.authenticate(http.HandlerFunc(s.getStorageSettings)))
		mux.Handle("PUT /v1/system/storage", s.authenticate(http.HandlerFunc(s.updateStorageSettings)))
		mux.Handle("POST /v1/system/storage/check", s.authenticate(http.HandlerFunc(s.checkStorageSettings)))
		mux.Handle("GET /v1/system/limits", s.authenticate(http.HandlerFunc(s.limitSettings)))
		mux.Handle("PUT /v1/system/limits", s.authenticate(http.HandlerFunc(s.limitSettings)))
		mux.Handle("GET /v1/system/markups", s.authenticate(http.HandlerFunc(s.markupSettings)))
		mux.Handle("PUT /v1/system/markups", s.authenticate(http.HandlerFunc(s.markupSettings)))
		mux.Handle("GET /v1/system/markups/catalog", s.authenticate(http.HandlerFunc(s.markupCatalog)))
		mux.Handle("GET /v1/system/settings", s.authenticate(http.HandlerFunc(s.systemSettings)))
		mux.Handle("PUT /v1/system/settings", s.authenticate(http.HandlerFunc(s.systemSettings)))
		mux.Handle("GET /v1/system/login-providers", s.authenticate(http.HandlerFunc(s.loginMethodSettings)))
		mux.Handle("PUT /v1/system/login-providers/{provider}", s.authenticate(http.HandlerFunc(s.loginMethodSettings)))
		mux.Handle("GET /v1/system/email", s.authenticate(http.HandlerFunc(s.emailDeliverySettings)))
		mux.Handle("PUT /v1/system/email", s.authenticate(http.HandlerFunc(s.emailDeliverySettings)))
		mux.Handle("GET /v1/system/catalog/documents/{hash}", s.authenticate(http.HandlerFunc(s.catalogSyncAdmin)))
		mux.Handle("GET /v1/system/catalog/settings", s.authenticate(http.HandlerFunc(s.catalogSyncAdmin)))
		mux.Handle("PUT /v1/system/catalog/settings", s.authenticate(http.HandlerFunc(s.catalogSyncAdmin)))
		mux.Handle("GET /v1/system/catalog/schedules", s.authenticate(http.HandlerFunc(s.catalogSyncAdmin)))
		mux.Handle("PUT /v1/system/catalog/schedules/{id}", s.authenticate(http.HandlerFunc(s.catalogSyncAdmin)))
		mux.Handle("POST /v1/system/catalog/schedules/{id}/check", s.authenticate(http.HandlerFunc(s.catalogSyncAdmin)))
		mux.Handle("GET /v1/system/catalog/runs", s.authenticate(http.HandlerFunc(s.catalogSyncAdmin)))
		mux.Handle("GET /v1/system/catalog/providers/{id}", s.authenticate(http.HandlerFunc(s.catalogSyncAdmin)))
		mux.Handle("GET /v1/system/catalog/providers/{id}/versions", s.authenticate(http.HandlerFunc(s.catalogSyncAdmin)))
		mux.Handle("GET /v1/system/users", s.authenticate(http.HandlerFunc(s.systemUsers)))
		mux.Handle("GET /v1/system/sales", s.authenticate(http.HandlerFunc(s.salesStructure)))
		mux.Handle("PUT /v1/system/sales/{employee}", s.authenticate(http.HandlerFunc(s.salesStructure)))
		mux.Handle("GET /v1/sales/bonuses", s.authenticate(http.HandlerFunc(s.salesBonuses)))
		mux.Handle("GET /v1/sales/bonuses/{employee}/entries", s.authenticate(http.HandlerFunc(s.salesBonuses)))
		mux.Handle("POST /v1/sales/bonuses/{employee}/payouts", s.authenticate(http.HandlerFunc(s.salesBonuses)))
		mux.Handle("PUT /v1/system/sales/bonus-settings", s.authenticate(http.HandlerFunc(s.salesBonuses)))
		mux.Handle("GET /v1/sales/team", s.authenticate(http.HandlerFunc(s.salesTeam)))
		mux.Handle("GET /v1/sales/permissions", s.authenticate(http.HandlerFunc(s.salesPermissions)))
		mux.Handle("PUT /v1/sales/permissions/{employee}/{permission}", s.authenticate(http.HandlerFunc(s.salesPermissions)))
		mux.Handle("PUT /v1/system/users/{id}/role", s.authenticate(http.HandlerFunc(s.setSystemAdmin)))
	}
	mux.HandleFunc("GET /openapi.json", openAPISpec)
	mux.HandleFunc("GET /openapi.yaml", openAPISpec)
	mux.HandleFunc("GET /provider-assets/{id}/icon.png", providerIcon)
	mux.HandleFunc("GET /provider-assets/{id}/icon.svg", providerSVGIcon)
	mux.HandleFunc("GET /healthz", s.healthz)
	if s.auth != nil {
		mux.Handle("GET /v1/info", s.authenticate(http.HandlerFunc(s.info)))
	} else {
		mux.HandleFunc("GET /v1/info", s.info)
	}
	if s.auth != nil {
		mux.HandleFunc("GET /auth/google", s.auth.Login)
		mux.HandleFunc("GET /auth/google/callback", s.auth.Callback)
		mux.HandleFunc("POST /auth/logout", s.auth.Logout)
		if emailAuth, ok := s.auth.(interface {
			Methods(http.ResponseWriter, *http.Request)
			Email(http.ResponseWriter, *http.Request)
		}); ok {
			mux.HandleFunc("GET /auth/methods", emailAuth.Methods)
			mux.HandleFunc("POST /auth/email/{action}", emailAuth.Email)
		}
		if setup, ok := s.auth.(interface {
			Setup(http.ResponseWriter, *http.Request)
		}); ok {
			mux.HandleFunc("POST /auth/setup", setup.Setup)
		}
	}
	mux.Handle("GET /v1/me", s.authenticate(http.HandlerFunc(s.me)))
	mux.Handle("PATCH /v1/me", s.authenticate(http.HandlerFunc(s.updateProfile)))
	s.registerReferenceRoutes(mux)
	s.registerProviderAssetRoutes(mux)
	if s.keys != nil {
		mux.Handle("GET /v1/key", s.authenticate(http.HandlerFunc(s.currentAPIKey)))
		mux.Handle("GET /v1/admin/reports/{id}", s.authenticate(http.HandlerFunc(s.reportDetail)))
		mux.Handle("GET /v1/admin/reports/{id}/content", s.authenticate(http.HandlerFunc(s.reportArtifact)))
		mux.Handle("HEAD /v1/admin/reports/{id}/content", s.authenticate(http.HandlerFunc(s.reportArtifact)))
		mux.Handle("GET /v1/admin/reports/{id}/last-frame", s.authenticate(http.HandlerFunc(s.reportArtifact)))
		mux.Handle("HEAD /v1/admin/reports/{id}/last-frame", s.authenticate(http.HandlerFunc(s.reportArtifact)))
		mux.Handle("GET /v1/admin/reports", s.authenticate(http.HandlerFunc(s.costReport)))
		mux.Handle("GET /v1/api-keys/billing", s.authenticate(http.HandlerFunc(s.billingStatus)))
		mux.Handle("GET /v1/api-keys/usage", s.authenticate(http.HandlerFunc(s.apiKeyUsage)))
		mux.Handle("GET /v1/api-keys", s.authenticate(http.HandlerFunc(s.listAPIKeys)))
		mux.Handle("POST /v1/api-keys", s.authenticate(http.HandlerFunc(s.createAPIKey)))
		mux.Handle("DELETE /v1/api-keys/{id}", s.authenticate(http.HandlerFunc(s.revokeAPIKey)))
	}
	mux.Handle("GET /v1/providers", s.authenticate(http.HandlerFunc(s.listProviders)))
	if s.providerAdmin != nil {
		mux.Handle("GET /v1/admin/providers/types", s.authenticate(http.HandlerFunc(s.providerTypes)))
		mux.Handle("POST /v1/admin/providers", s.authenticate(http.HandlerFunc(s.createAdminProvider)))
		mux.Handle("GET /v1/admin/providers", s.authenticate(http.HandlerFunc(s.listAdminProviders)))
		mux.Handle("GET /v1/admin/providers/{id}", s.authenticate(http.HandlerFunc(s.getAdminProvider)))
		mux.Handle("PUT /v1/admin/providers/{id}", s.authenticate(http.HandlerFunc(s.updateAdminProvider)))
		mux.Handle("POST /v1/admin/providers/{id}/check", s.authenticate(http.HandlerFunc(s.checkAdminProvider)))
	}
	if s.storageAdmin != nil {
		mux.Handle("GET /v1/admin/storage", s.authenticate(http.HandlerFunc(s.getStorageSettings)))
		mux.Handle("PUT /v1/admin/storage", s.authenticate(http.HandlerFunc(s.updateStorageSettings)))
		mux.Handle("POST /v1/admin/storage/check", s.authenticate(http.HandlerFunc(s.checkStorageSettings)))
	}
	s.registerOpenAIRoutes(mux)
	mux.HandleFunc("GET /media/{id}", s.serveSignedMedia)
	mux.HandleFunc("HEAD /media/{id}", s.serveSignedMedia)
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusForbidden, "cross_origin_request", "cross-origin browser mutations are not allowed")
	}))
	return requestLogging(recoverPanic(protection.Handler(mux)))
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	info := s.service.Info()
	if s.modelAccess != nil {
		catalog, err := s.applyModelAccess(r.Context(), s.service.ListProviders(r.Context()), false)
		if err != nil {
			writeError(w, 503, "model_settings_unavailable", "model availability could not be checked")
			return
		}
		ctx := context.WithValue(r.Context(), modelCatalogKey{}, catalog)
		catalog = s.visibleCatalog(r.WithContext(ctx))
		preferred := info.Provider
		info.Provider, info.Model, info.Live = "", "", false
		for _, p := range catalog {
			if info.Provider != "" && p.ID != preferred {
				continue
			}
			for _, m := range p.Models {
				if m.ID == p.DefaultModel {
					info.Provider, info.Model, info.Live = p.ID, m.PublicID, p.Live
					break
				}
			}
		}
	}
	if s.auth != nil {
		info.AuthMode = "google"
	} else {
		info.AuthMode = "api_key"
	}
	settings := system.Defaults()
	if s.system != nil {
		var err error
		settings, err = s.system.Settings(r.Context())
		if err != nil {
			systemError(w, err)
			return
		}
	}
	s.writeViewerJSON(w, r, http.StatusOK, struct {
		core.ServiceInfo
		Console any `json:"console"`
	}{info, settings.Public()})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user, ok := identity.UserFromContext(r.Context())
	if !ok || user.ID == identity.SystemOwnerID {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sign-in is required")
		return
	}
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	manager, err := s.isClientManager(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	salesRole := ""
	if repo, ok := s.system.(system.SalesRepository); ok && manager {
		salesRole, err = repo.SalesRole(r.Context(), user.ID)
		if err != nil {
			systemError(w, err)
			return
		}
	}
	s.writeViewerJSON(w, r, http.StatusOK, struct {
		identity.User
		SystemAdmin   bool   `json:"is_system_admin"`
		ClientManager bool   `json:"is_client_manager"`
		SalesRole     string `json:"sales_role,omitempty"`
	}{user, admin, manager, salesRole})
}

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	catalog := s.visibleCatalog(r)
	s.writeViewerJSON(w, r, http.StatusOK, map[string]any{"data": catalog})
}

func (s *Server) submitGeneration(w http.ResponseWriter, r *http.Request, input core.CreateMediaInput) {
	task, err := s.service.CreateMediaRequest(r.Context(), input, r.Header.Get("Idempotency-Key"))
	switch {
	case errors.Is(err, core.ErrCostLimitInvalid):
		invalidParameter(w, "max_expected_cost_usd", err.Error())
	case errors.Is(err, core.ErrPromptRequired), errors.Is(err, core.ErrPromptTooLong), errors.Is(err, core.ErrModelRequired),
		errors.Is(err, core.ErrProviderUnavailable), errors.Is(err, core.ErrModelUnavailable), errors.Is(err, core.ErrOptionsInvalid),
		errors.Is(err, core.ErrWebhookInvalid), errors.Is(err, core.ErrIdempotencyKey):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, core.ErrBudgetExceeded):
		writeError(w, http.StatusPaymentRequired, "budget_exceeded", core.ErrBudgetExceeded.Error())
	case errors.Is(err, core.ErrPricingUnavailable):
		writeError(w, http.StatusUnprocessableEntity, "pricing_unavailable", core.ErrPricingUnavailable.Error())
	case errors.Is(err, core.ErrEstimateChanged):
		writeError(w, http.StatusConflict, "estimate_changed", core.ErrEstimateChanged.Error())
	case errors.Is(err, core.ErrKeyUnavailable):
		writeError(w, http.StatusUnauthorized, "unauthorized", core.ErrKeyUnavailable.Error())
	case errors.Is(err, core.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", core.ErrIdempotencyConflict.Error())
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "task could not be accepted")
	default:
		view := core.GenerationView(task)
		w.Header().Set("Location", view.StatusURL)
		s.writeGenerationJSON(w, r, http.StatusAccepted, task)
	}
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.health.Check(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	next = s.auditRequest(s.clientManagerPolicy(s.organizationPolicy(s.systemPolicy(s.requestLimits(s.modelCatalogPolicy(next))))))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// An explicit credential must never silently fall back to a browser session.
		bearer := r.Header.Get("Authorization")
		hasBearer, hasXKey := len(r.Header.Values("Authorization")) > 0, len(r.Header.Values("X-API-Key")) > 0
		if hasXKey || len(r.Header.Values("Authorization")) > 1 {
			writeError(w, http.StatusBadRequest, "invalid_request", "use one Authorization: Bearer header")
			return
		}
		provided := ""
		if hasBearer {
			parts := strings.Fields(bearer)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				writeError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer credential")
				return
			}
			provided = parts[1]
		}
		management := strings.HasPrefix(r.URL.Path, "/v1/sales/") || strings.HasPrefix(r.URL.Path, "/v1/client-onboardings") || strings.HasPrefix(r.URL.Path, "/v1/organizations") || strings.HasPrefix(r.URL.Path, "/v1/invitations") || strings.HasPrefix(r.URL.Path, "/v1/system/") || strings.HasPrefix(r.URL.Path, "/v1/audit/") || r.URL.Path == "/v1/me" || r.URL.Path == "/v1/info" || strings.HasPrefix(r.URL.Path, "/v1/admin/") || r.URL.Path == "/v1/api-keys" || strings.HasPrefix(r.URL.Path, "/v1/api-keys/")
		if !management && !hasBearer && !hasXKey {
			writeError(w, http.StatusUnauthorized, "unauthorized", "a Gateway API key is required; browser sign-in does not authorize generation")
			return
		}
		if !hasBearer && !hasXKey && s.auth != nil && management {
			if user, err := s.auth.Authenticate(r); err == nil {
				next.ServeHTTP(w, r.WithContext(identity.WithUser(r.Context(), user)))
				return
			}
		}
		if provided != "" && s.apiKey != "" && len(provided) == len(s.apiKey) && subtle.ConstantTimeCompare([]byte(provided), []byte(s.apiKey)) == 1 {
			next.ServeHTTP(w, r.WithContext(identity.WithOwnerID(r.Context(), identity.SystemOwnerID)))
			return
		}
		if s.keys != nil && strings.HasPrefix(provided, "gw_") && len(provided) == 46 {
			user, key, err := s.keys.Authenticate(r.Context(), apikey.Hash(provided))
			if err == nil {
				scope := apikey.Generation
				if (strings.HasPrefix(r.URL.Path, "/v1/organizations") && r.Method != http.MethodGet && r.Method != http.MethodHead) || r.URL.Path == "/v1/invitations/accept" {
					scope = apikey.OrganizationsManage
				}
				if strings.HasPrefix(r.URL.Path, "/v1/organizations/") && (strings.HasSuffix(r.URL.Path, "/storage") || strings.Contains(r.URL.Path, "/payments") || strings.HasSuffix(r.URL.Path, "/payment-methods") || strings.HasSuffix(r.URL.Path, "/balance-history")) {
					scope = apikey.OrganizationsManage
				}
				if strings.HasPrefix(r.URL.Path, "/v1/audit/") || strings.HasPrefix(r.URL.Path, "/v1/system/") || (strings.HasPrefix(r.URL.Path, "/v1/organizations/") && (strings.HasSuffix(r.URL.Path, "/markups") || strings.HasSuffix(r.URL.Path, "/limits") || strings.HasSuffix(r.URL.Path, "/model-access"))) {
					scope = apikey.SystemManage
				}
				if strings.HasPrefix(r.URL.Path, "/v1/admin/providers") {
					scope = apikey.ProvidersManage
				}
				if strings.HasPrefix(r.URL.Path, "/v1/admin/storage") {
					scope = apikey.StorageManage
				}
				if strings.HasPrefix(r.URL.Path, "/v1/api-keys") || strings.HasPrefix(r.URL.Path, "/v1/admin/reports") {
					scope = apikey.KeysManage
				}
				if strings.HasPrefix(r.URL.Path, "/v1/organizations/") && strings.Contains(r.URL.Path, "/audit/") {
					scope = apikey.OrganizationsManage
				}
				if !slices.Contains(key.Scopes, scope) {
					writeError(w, http.StatusForbidden, "insufficient_scope", "API key requires scope: "+scope)
					if auditMutation(r) {
						ctx := audit.WithCorrelation(identity.WithAPIKeyID(identity.WithUser(r.Context(), user), key.ID), uuid.NewString())
						s.recordAuditFailure(r.WithContext(ctx), 403)
					}
					return
				}
				ctx := identity.WithAPIKeyID(identity.WithUser(r.Context(), user), key.ID)
				ctx = identity.WithSystemPermission(ctx, slices.Contains(key.Scopes, apikey.SystemManage))
				if !management {
					provider, storage := user.ID, user.ID
					if key.ResourceMode == "shared" {
						storage = identity.SharedOwnerID
						provider = identity.SharedOwnerID
					}

					ctx = identity.WithResourceOwners(ctx, provider, storage)
					if key.OrganizationID == "" {
						ctx = identity.WithPersonalData(ctx)
					}
					if key.OrganizationID != "" {
						ctx = identity.WithOrganization(ctx, key.OrganizationID, key.OrganizationAdmin)
						if key.OrganizationRole != "" {
							ctx = identity.WithOrganizationRole(ctx, key.OrganizationID, key.OrganizationRole)
						}
						if s.storageAdmin != nil && key.ResourceMode == "shared" {
							orgCtx := identity.WithResourceOwners(ctx, provider, key.OrganizationID)
							current, e := s.storageAdmin.Get(orgCtx)
							if e != nil {
								writeError(w, 503, "storage_unavailable", "organization storage could not be read")
								return
							}
							storage = identity.SharedOwnerID
							if current.Driver == "s3" {
								storage = key.OrganizationID
							}
							ctx = identity.WithResourceOwners(ctx, provider, storage)
						}
					}
				}
				ctx = context.WithValue(ctx, apiKeyScopesContextKey{}, key.Scopes)
				ctx = context.WithValue(ctx, credentialOrganizationKey{}, key.OrganizationID)
				if slices.Contains(key.Scopes, apikey.SystemManage) && s.system != nil {
					global, e := s.system.IsAdmin(ctx, user.ID)
					if e != nil {
						systemError(w, e)
						return
					}
					ctx = context.WithValue(ctx, globalOrganizationKey{}, global)
				}
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			if !errors.Is(err, apikey.ErrNotFound) {
				writeError(w, http.StatusServiceUnavailable, "service_unavailable", "API key could not be verified")
				return
			}
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "sign in or provide a valid Gateway API key")
	})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeAPIError(w, status, code, message, nil)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
