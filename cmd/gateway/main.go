package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	userauth "github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/config"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/Sociaro-Research/open-media-gateway/internal/monitoring"
	"github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/playgroundkey"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/providers"
	"github.com/Sociaro-Research/open-media-gateway/internal/queue"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	stripepayment "github.com/Sociaro-Research/open-media-gateway/plugins/payment/stripe"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		if err := adminCommand(os.Args[2:]); err != nil {
			slog.Error("admin command failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := healthcheck(); err != nil {
			slog.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func healthcheck() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", response.Status)
	}
	return nil
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if cfg.Migrate == "auto" {
		if err := postgres.Migrate(db); err != nil {
			return err
		}
	}
	redisQueue, err := queue.Open(ctx, cfg.RedisURL, "media")
	if err != nil {
		return err
	}
	defer redisQueue.Close()

	repository := postgres.NewTaskRepository(db)
	credentialsCipher, err := providerconfig.NewCipher(cfg.ProviderCredentialsKey)
	if err != nil {
		return err
	}
	providerManager := providers.NewManager(postgres.NewProviderSettingsRepository(db), credentialsCipher, cfg)
	catalogRepository := postgres.NewCatalogRepository(db)
	providerManager.SetCatalog(catalogRepository)
	providerManager.SetSystemSettings(postgres.NewSystemRepository(db))
	if err := providerManager.Bootstrap(ctx); err != nil {
		return fmt.Errorf("bootstrap provider settings: %w", err)
	}
	availableProviders := providerManager.ManagedProviders()
	defaults := cfg.ProviderModels()
	if cfg.DefaultMediaModel != "" {
		defaults[cfg.MediaProvider] = cfg.DefaultMediaModel
	}
	service := core.NewTaskServiceWithProviders(repository, redisQueue, availableProviders, defaults, cfg.MediaProvider)
	service.SetProviderDirectory(providerManager)
	service.EnableAccounting()
	catalogCtx, cancelCatalog := context.WithTimeout(ctx, 5*time.Second)
	_ = service.ListProviders(catalogCtx)
	cancelCatalog()
	mediaStorage, err := storage.NewManager(postgres.NewStorageSettingsRepository(db), credentialsCipher, cfg.StorageLocalPath, httpclient.NewPublic(cfg.StorageDownloadTimeout, cfg.OutboundPrivateHosts...), cfg.StorageMaxBytes)
	if err != nil {
		return fmt.Errorf("configure result storage: %w", err)
	}
	var authManager *userauth.Manager
	emailSender := userauth.NewDeliveryManager(postgres.NewSystemRepository(db), credentialsCipher)
	if err := emailSender.Bootstrap(ctx, cfg.ResendAPIKey, cfg.EmailFrom); err != nil {
		return fmt.Errorf("bootstrap email delivery: %w", err)
	}
	if err := providerManager.EnsureCatalogSchedules(ctx); err != nil {
		return fmt.Errorf("bootstrap catalogue synchronization: %w", err)
	}
	catalogRunner := &catalogsync.Runner{Repo: catalogRepository, Providers: providerManager, Client: httpclient.NewPublic(30 * time.Second), Mailer: emailSender}
	go catalogRunner.Run(ctx)
	loginProviders := userauth.NewLoginProviderManager(postgres.NewSystemRepository(db), credentialsCipher, cfg.AuthPublicURL)
	if err := loginProviders.Bootstrap(ctx, cfg.GoogleOAuthClientID, cfg.GoogleOAuthClientSecret, cfg.GoogleOAuthRedirectURL); err != nil {
		return fmt.Errorf("bootstrap login providers: %w", err)
	}
	if cfg.SessionSigningSecret != "" {
		authCtx, cancelAuth := context.WithTimeout(ctx, 10*time.Second)
		authManager, err = userauth.New(authCtx, postgres.NewAuthRepository(db), userauth.Config{
			LoginProviders: loginProviders, SessionSecret: cfg.SessionSigningSecret,
			SessionTTL: cfg.SessionTTL, PublicURL: cfg.AuthPublicURL, UIURL: cfg.UIPublicURL, EmailSender: emailSender,
			SetupCipher: credentialsCipher,
		})
		cancelAuth()
		if err != nil {
			return fmt.Errorf("configure authentication: %w", err)
		}
	}
	funding := payments.New(postgres.NewSystemRepository(db), credentialsCipher, cfg.AuthPublicURL, map[string]payments.Adapter{"stripe": stripepayment.New()})
	funding.UIURL = cfg.UIPublicURL
	funding.Chain = payments.NewEVMReader()
	go funding.Run(ctx)
	go funding.RunNotifications(ctx, userauth.NewDeliveryManager(postgres.NewSystemRepository(db), credentialsCipher))
	var onboarding *organization.Onboarding
	if authManager != nil {
		origin := cfg.UIPublicURL
		if origin == "" {
			origin = cfg.AuthPublicURL
		}
		onboarding, err = organization.NewOnboarding(postgres.NewSystemRepository(db), emailSender, origin, cfg.SessionSigningSecret)
		if err != nil {
			return fmt.Errorf("configure company onboarding: %w", err)
		}
		go onboarding.Run(ctx)
	}
	assetService := providerassets.New(postgres.NewProviderAssetRepository(db), providerManager)
	go assetService.Run(ctx)
	templateRepository := postgres.NewTemplateRepository(db)
	defer templateRepository.Close()
	playgroundKeys, err := playgroundkey.New(postgres.NewAPIKeyRepository(db), postgres.NewAPIKeyRepository(db), cfg.ProviderCredentialsKey)
	if err != nil {
		return err
	}
	handler := api.NewGateway(api.Dependencies{
		ResourceRepository: postgres.NewProviderResourceRepository(db), ResourceScopes: providerManager.ResolveResourceConnection, ResourceDirectory: providerManager,
		PlaygroundKeys:         playgroundKeys,
		CatalogSync:            catalogRepository,
		WorkspaceAuthorization: wa.New(postgres.NewSystemRepository(db), credentialsCipher),
		RemoteWorkspace:        remoteworkspace.New(postgres.NewSystemRepository(db), credentialsCipher, httpclient.NewPublic(30*time.Second)),
		Service:                service, Health: api.NewDependenciesHealth(db, redisQueue), APIKey: cfg.ClientAPIKey,
		Storage: mediaStorage, SigningSecret: cfg.MediaURLSigningSecret, MediaURLTTL: cfg.MediaURLTTL,
		ProviderAssets: assetService, AssetPublicURL: cfg.AuthPublicURL,
		Templates:     templateRepository,
		ProviderAdmin: providerManager, Auth: authManager, References: references.New(postgres.NewReferenceRepository(db), mediaStorage),
		System: postgres.NewSystemRepository(db), Payments: funding,
		Keys:           api.WithRequestLimits(postgres.NewAPIKeyRepository(db), postgres.NewSystemRepository(db), redisQueue),
		LoginProviders: loginProviders, EmailDelivery: emailSender,
		Onboarding: onboarding,
	})
	handler, err = api.WithPublicAPI(handler, cfg.AuthPublicURL, cfg.UIPublicURL, cfg.DocsPublicURL, postgres.NewSystemRepository(db))
	if err != nil {
		return fmt.Errorf("configure API origins: %w", err)
	}
	var metricsErrors <-chan error
	if cfg.MetricsAddr != "" {
		metrics := monitoring.New("gateway", monitoring.Dependencies{Database: db, Queues: map[string]monitoring.Queue{"media": redisQueue}, Tasks: repository.MonitoringStats})
		metricsServer, err := metrics.Listen(cfg.MetricsAddr)
		if err != nil {
			return fmt.Errorf("listen for metrics: %w", err)
		}
		defer metricsServer.Close()
		metricsErrors = metricsServer.Errors
		handler = metrics.InstrumentHTTP(handler)
		slog.Info("metrics listening", "address", cfg.MetricsAddr)
	}
	server := api.NewHTTPServer(":"+cfg.Port, handler)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("gateway listening", "address", server.Addr, "provider", cfg.MediaProvider)
		errCh <- server.ListenAndServe()
	}()
	select {
	case err := <-metricsErrors:
		_ = server.Close()
		return fmt.Errorf("metrics server: %w", err)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
