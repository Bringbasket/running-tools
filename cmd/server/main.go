package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	platformauth "github.com/Bringbasket/running-tools/internal/platform/auth"
	platformconfig "github.com/Bringbasket/running-tools/internal/platform/config"
	"github.com/Bringbasket/running-tools/internal/platform/httpx"
	"github.com/Bringbasket/running-tools/internal/platform/module"
	"github.com/Bringbasket/running-tools/internal/platform/persistence"
	"github.com/Bringbasket/running-tools/internal/platform/systemupdate"
	"github.com/Bringbasket/running-tools/internal/webui"
	mail "github.com/Bringbasket/running-tools/modules/mail/backend"
)

var (
	buildVersion  string
	buildRevision string
)

func main() {
	// Local development reads .env; deployed environments continue to override it.
	_ = platformconfig.LoadEnvFile(".env")
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config, err := platformconfig.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	if value := strings.TrimSpace(buildVersion); value != "" {
		config.Version = value
	}
	if value := strings.TrimSpace(buildRevision); value != "" {
		config.Revision = value
	}
	persistenceConfig, err := persistence.LoadConfig()
	if err != nil {
		logger.Error("invalid persistence configuration", "error", err)
		os.Exit(1)
	}
	startupContext, startupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	persistenceService, err := persistence.Open(startupContext, persistenceConfig)
	startupCancel()
	if err != nil {
		logger.Error("initialize persistence", "error", err)
		os.Exit(1)
	}
	defer persistenceService.Close()
	authService, err := platformauth.NewService(
		persistenceService.DB(), persistenceService.Redis(), persistenceService.RedisPrefix(),
		platformauth.Config{
			AdminUsername: config.AdminUsername,
			SessionTTL:    config.AuthSessionTTL,
			TrustProxy:    config.TrustProxy,
		},
	)
	if err != nil {
		logger.Error("initialize authentication", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	passthrough := func(next http.Handler) http.Handler { return next }
	platformauth.NewHTTP(authService).RegisterRoutes(mux)
	mailModule, err := mail.NewModuleWithPersistence(
		filepath.Join(config.DataDir, "mail"),
		os.Getenv("MAIL_CONFIG_PATH"), os.Getenv("MAIL_STATE_DIR"), persistenceService,
	)
	if err != nil {
		logger.Error("initialize mail module", "error", err)
		os.Exit(1)
	}
	modules := []module.Backend{mailModule}
	for _, backend := range modules {
		backend.RegisterRoutes(mux, passthrough)
		backend.Start()
	}
	defer func() {
		for _, backend := range modules {
			backend.Stop()
		}
	}()

	updates := systemupdate.New(filepath.Join(config.DataDir, "system"), config.Version, config.Revision, config.RepositoryURL)
	systemupdate.RegisterRoutes(mux, passthrough, updates)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		data, healthy := persistenceService.Health(ctx)
		status := http.StatusOK
		data["status"] = "ok"
		if !healthy {
			status = http.StatusServiceUnavailable
			data["status"] = "error"
		}
		httpx.WriteData(w, r, status, data)
	})
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "not found")
	}))
	mux.Handle("/v1/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "not found")
	}))
	ui, err := webui.NewHandler()
	if err != nil {
		logger.Error("load embedded frontend", "error", err)
		os.Exit(1)
	}
	mux.Handle("/", ui)

	handler := httpx.Chain(authService.ProtectAPIs(mux), httpx.RequestIDs, httpx.SecurityHeaders, httpx.AccessLog(logger), httpx.Recover(logger))
	server := &http.Server{Addr: config.Address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second}
	cleanupContext, stopCleanup := context.WithCancel(context.Background())
	defer stopCleanup()
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			cleanupCtx, cleanupCancel := context.WithTimeout(cleanupContext, 30*time.Second)
			if cleanupErr := authService.Cleanup(cleanupCtx); cleanupErr != nil && cleanupContext.Err() == nil {
				logger.Warn("clean expired authentication records", "error", cleanupErr)
			}
			cleanupCancel()
			select {
			case <-ticker.C:
			case <-cleanupContext.Done():
				return
			}
		}
	}()
	go func() {
		logger.Info("server listening", "address", config.Address, "version", config.Version, "revision", config.Revision)
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("server stopped", "error", serveErr)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	stopCleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown failed", "error", err)
	}
}
