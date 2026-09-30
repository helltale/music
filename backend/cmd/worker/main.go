package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/helltale/music/backend/internal/catalog/provider"
	"github.com/helltale/music/backend/internal/importjob"
	"github.com/helltale/music/backend/internal/platform"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		addr := os.Getenv("WORKER_HEALTH_ADDR")
		if addr == "" {
			addr = ":8081"
		}
		os.Exit(platform.ProbeGET(addr, "/health"))
	}

	log := platform.NewLogger()
	slog.SetDefault(log)

	cfg, err := platform.Load()
	if err != nil {
		log.Error("configuration", "error", err.Error())
		os.Exit(1)
	}
	log = log.With("worker_instance_id", cfg.WorkerInstanceID)
	slog.SetDefault(log)

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
	mux.HandleFunc("/", platform.NotFound)
	srv := platform.NewServer(cfg.WorkerHealthAddr, platform.Middleware(log, mux))

	worker := &importjob.Worker{
		Store: importjob.NewStore(db),
		DB:    db,
		Catalogs: map[string]importjob.CatalogSource{
			provider.ProviderFake: provider.NewFake(),
		},
		WorkerID:  cfg.WorkerInstanceID,
		Lease:     cfg.LeaseTimeout,
		Heartbeat: cfg.LeaseHeartbeat,
		Poll:      time.Second,
		Log:       log,
	}
	workerDone := make(chan struct{})
	go func() {
		if err := worker.Run(ctx); err != nil {
			log.Error("worker", "error", err.Error())
		}
		close(workerDone)
	}()

	errCh := make(chan error, 1)
	go func() {
		log.Info("worker listening", "addr", cfg.WorkerHealthAddr)
		errCh <- platform.ListenErr(srv.ListenAndServe())
	}()

	signaled := true
	select {
	case <-ctx.Done():
	case err := <-errCh:
		signaled = false
		stop()
		if err != nil {
			log.Error("server", "error", err.Error())
		}
	}

	ready.ShuttingDown.Store(true)
	log.Info("shutdown started")
	shutCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	select {
	case <-workerDone:
	case <-shutCtx.Done():
		log.Error("worker shutdown timed out")
	}
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Error("shutdown", "error", err.Error())
		os.Exit(1)
	}
	if signaled {
		<-errCh
	}
	log.Info("shutdown complete")
}
