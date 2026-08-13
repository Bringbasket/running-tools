package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	platformconfig "github.com/Bringbasket/running-tools/internal/platform/config"
	"github.com/Bringbasket/running-tools/internal/platform/httpx"
	"github.com/Bringbasket/running-tools/internal/platform/module"
	"github.com/Bringbasket/running-tools/internal/platform/persistence"
	"github.com/Bringbasket/running-tools/internal/platform/systemupdate"
	"github.com/Bringbasket/running-tools/internal/webui"
	mail "github.com/Bringbasket/running-tools/modules/mail/backend"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config, err := platformconfig.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
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

	mux := http.NewServeMux()
	auth := httpx.APIKey(config.APIKey)
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
		backend.RegisterRoutes(mux, auth)
		backend.Start()
	}
	defer func() {
		for _, backend := range modules {
			backend.Stop()
		}
	}()

	updates := systemupdate.New(filepath.Join(config.DataDir, "system"), config.Version, config.RepositoryURL)
	systemupdate.RegisterRoutes(mux, auth, updates)
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

	handler := httpx.Chain(mux, httpx.RequestIDs, httpx.SecurityHeaders, httpx.AccessLog(logger), httpx.Recover(logger))
	server := &http.Server{Addr: config.Address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		logger.Info("server listening", "address", config.Address, "version", config.Version)
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("server stopped", "error", serveErr)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown failed", "error", err)
	}
}
