package platform

import (
	"log/slog"
	"os"
)

// NewLogger writes JSON logs to stdout.
func NewLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
