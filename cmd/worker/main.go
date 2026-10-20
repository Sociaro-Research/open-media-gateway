package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/config"
	"github.com/Sociaro-Research/media-gateway-dev/internal/httpclient"
	"github.com/Sociaro-Research/media-gateway-dev/internal/monitoring"
	"github.com/Sociaro-Research/media-gateway-dev/internal/postgres"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providers"
	"github.com/Sociaro-Research/media-gateway-dev/internal/queue"
	"github.com/Sociaro-Research/media-gateway-dev/internal/references"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"github.com/Sociaro-Research/media-gateway-dev/internal/webhook"
	workerprocess "github.com/Sociaro-Research/media-gateway-dev/internal/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker stopped", "error", err)
		os.Exit(1)
	}
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
	webhookQueue, err := queue.Open(ctx, cfg.RedisURL, "webhook")
	if err != nil {
		return err
	}
	defer webhookQueue.Close()
	credentialsCipher, err := providerconfig.NewCipher(cfg.ProviderCredentialsKey)
	if err != nil {
		return err
	}
	mediaStorage, err := storage.NewManager(postgres.NewStorageSettingsRepository(db), credentialsCipher, cfg.StorageLocalPath, httpclient.NewPublic(cfg.StorageDownloadTimeout, cfg.OutboundPrivateHosts...), cfg.StorageMaxBytes)
	if err != nil {
		return fmt.Errorf("configure result storage: %w", err)
	}

	referenceLibrary := references.New(postgres.NewReferenceRepository(db), mediaStorage)
	providerManager := providers.NewManager(postgres.NewProviderSettingsRepository(db), credentialsCipher, cfg, references.NewResolver(referenceLibrary, cfg.MediaURLSigningSecret))
	providerManager.SetCatalog(postgres.NewCatalogRepository(db))
	providerManager.SetSystemSettings(postgres.NewSystemRepository(db))
	if err := providerManager.Bootstrap(ctx); err != nil {
		return fmt.Errorf("bootstrap provider settings: %w", err)
	}
	availableProviders := providerManager.ManagedProviders()

	slog.Info("worker started", "providers", len(availableProviders))
	taskWorker := workerprocess.NewWithProviders(
		postgres.NewTaskRepository(db), redisQueue, availableProviders, mediaStorage,
		cfg.PollInterval, cfg.ProviderTaskTimeout, cfg.MaxPollErrors, cfg.DispatchInterval,
	)
	taskWorker.Directory = providerManager
	taskWorker.OperationConcurrency = cfg.OperationConcurrency
	taskWorker.ArchiveConcurrency = cfg.ArchiveConcurrency
	taskWorker.ShutdownTimeout = cfg.WorkerShutdownTimeout
	repository := postgres.NewTaskRepository(db)
	webhookSender := webhook.NewSender(cfg.WebhookSigningSecret, webhook.NewHTTPClient(cfg.WebhookHTTPTimeout))
	webhookWorker := webhook.NewWorker(repository, webhookQueue, webhookSender, cfg.WebhookMaxAttempts, cfg.WebhookRetryBase)
	var metricsErrors <-chan error
	if cfg.MetricsAddr != "" {
		metrics := monitoring.New("worker", monitoring.Dependencies{Database: db, Queues: map[string]monitoring.Queue{"media": redisQueue, "webhook": webhookQueue}})
		metrics.SetWorkerDispatchInterval(cfg.DispatchInterval)
		metricsServer, err := metrics.Listen(cfg.MetricsAddr)
		if err != nil {
			return fmt.Errorf("listen for metrics: %w", err)
		}
		defer metricsServer.Close()
		metricsErrors = metricsServer.Errors
		taskWorker.Observer = metrics
		slog.Info("metrics listening", "address", cfg.MetricsAddr)
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 2)
	go repository.RunBillingSync(workerCtx, providerManager)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			cleanup, err := mediaStorage.CleanupTemporaryFiles(workerCtx)
			if err != nil && workerCtx.Err() == nil {
				slog.Warn("storage temporary file cleanup", "error", err)
			}
			if cleanup.Removed > 0 || cleanup.RemovedDirectories > 0 {
				slog.Info("storage temporary files removed", "files", cleanup.Removed, "directories", cleanup.RemovedDirectories, "bytes", cleanup.RemovedBytes)
			}
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	go func() {
		for {
			if err := referenceLibrary.CleanupUploads(workerCtx); err != nil {
				slog.Warn("reference upload cleanup", "error", err)
			}
			if err := mediaStorage.ReconcileUsage(workerCtx); err != nil {
				slog.Warn("storage usage reconciliation", "error", err)
			}
			select {
			case <-workerCtx.Done():
				return
			case <-time.After(time.Minute):
			}
		}
	}()
	go func() { errCh <- taskWorker.Run(workerCtx) }()
	go func() { errCh <- webhookWorker.Run(workerCtx) }()
	select {
	case err = <-errCh:
	case err = <-metricsErrors:
		cancel()
		<-errCh
		<-errCh
		return fmt.Errorf("metrics server: %w", err)
	}
	cancel()
	other := <-errCh
	if err == nil {
		err = other
	}
	return err
}
