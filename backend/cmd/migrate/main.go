package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/helltale/music/backend/internal/platform"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	log := platform.NewLogger()
	slog.SetDefault(log)

	cfg, err := platform.Load()
	if err != nil {
		log.Error("configuration", "error", err.Error())
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	pool, err := openWithRetry(ctx, log, cfg.DatabaseURL)
	if err != nil {
		log.Error("database", "error", err.Error())
		os.Exit(1)
	}
	defer pool.Close()

	applied, err := platform.Apply(ctx, pool, cfg.MigrationsDir)
	if err != nil {
		log.Error("migrate", "error", err.Error())
		os.Exit(1)
	}
	log.Info("migrations applied", "count", applied)
}

func openWithRetry(ctx context.Context, log *slog.Logger, databaseURL string) (*pgxpool.Pool, error) {
	var lastErr error
	for attempt := 1; attempt <= 15; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pool, err := platform.OpenPostgres(ctx, databaseURL)
		if err == nil {
			pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err = pool.Ping(pingCtx)
			cancel()
			if err == nil {
				return pool, nil
			}
			pool.Close()
		}
		lastErr = err
		log.Info("waiting for database", "attempt", attempt)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, lastErr
}
