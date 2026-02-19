package logging

import (
	"io"
	"log/slog"
	"os"
)

// Config holds logging configuration.
type Config struct {
	Level  slog.Level
	Format string // "json" or "text"
	Output io.Writer
}

// New creates a structured logger with the given configuration.
func New(cfg Config) *slog.Logger {
	if cfg.Output == nil {
		cfg.Output = os.Stderr
	}

	opts := &slog.HandlerOptions{
		Level:     cfg.Level,
		AddSource: cfg.Level == slog.LevelDebug,
	}

	var handler slog.Handler
	switch cfg.Format {
	case "json":
		handler = slog.NewJSONHandler(cfg.Output, opts)
	default:
		handler = slog.NewTextHandler(cfg.Output, opts)
	}

	return slog.New(handler)
}
