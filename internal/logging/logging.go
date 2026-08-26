package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// New returns a JSON slog logger. Callers must never log keys, passwords, or tokens.
func New(level string, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lv})
	return slog.New(h)
}
