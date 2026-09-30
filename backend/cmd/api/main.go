package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/helltale/music/backend/internal/catalog"
	"github.com/helltale/music/backend/internal/catalog/provider"
	"github.com/helltale/music/backend/internal/platform"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(platform.ProbeGET(getenv("HTTP_ADDR", ":8080"), "/health"))
	}

	log := platform.NewLogger()
	slog.SetDefault(log)

	cfg, err := platform.Load()
	if err != nil {
		log.Error("configuration", "error", err.Error())
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	db, err := platform.OpenPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database", "error", err.Error())
		os.Exit(1)
	}
	defer db.Close()

	ready := &platform.Ready{DB: db}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", platform.Health)
	mux.HandleFunc("GET /ready", ready.ServeHTTP)
	catalog.Mount(mux, provider.NewFake())
	mux.HandleFunc("/", platform.NotFound)

	srv := platform.NewServer(cfg.HTTPAddr, platform.Middleware(log, mux))
	errCh := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", cfg.HTTPAddr)
		errCh <- platform.ListenErr(srv.ListenAndServe())
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil {
			log.Error("server", "error", err.Error())
			os.Exit(1)
		}
		return
	}

	ready.ShuttingDown.Store(true)
	log.Info("shutdown started")
	shutCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Error("shutdown", "error", err.Error())
		os.Exit(1)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server", "error", err.Error())
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
