package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const schemaMigrationsSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
)`

// MigrationFiles returns *.up.sql files in dir, sorted by file name.
func MigrationFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".up.sql") {
			files = append(files, filepath.Join(dir, name))
		}
	}
	sort.Strings(files)
	return files, nil
}

// Apply runs pending SQL migrations. An empty directory succeeds and applies nothing.
func Apply(ctx context.Context, pool *pgxpool.Pool, dir string) (int, error) {
	if _, err := pool.Exec(ctx, schemaMigrationsSQL); err != nil {
		return 0, fmt.Errorf("create schema_migrations: %w", err)
	}
	files, err := MigrationFiles(dir)
	if err != nil {
		return 0, err
	}
	applied := 0
	for _, file := range files {
		version := filepath.Base(file)
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`,
			version,
		).Scan(&exists); err != nil {
			return applied, fmt.Errorf("lookup %s: %w", version, err)
		}
		if exists {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			return applied, fmt.Errorf("read %s: %w", version, err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return applied, err
		}
		sqlText := strings.TrimSpace(string(body))
		if sqlText != "" {
			if _, err := tx.Conn().PgConn().Exec(ctx, sqlText).ReadAll(); err != nil {
				_ = tx.Rollback(ctx)
				return applied, fmt.Errorf("apply %s: %w", version, err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return applied, fmt.Errorf("record %s: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return applied, fmt.Errorf("commit %s: %w", version, err)
		}
		applied++
	}
	return applied, nil
}
