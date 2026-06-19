package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// SetupLogger initializes the structured logger (slog) with console and optional file output.
func SetupLogger(levelStr string, logFilePath string) (*slog.Logger, error) {
	var level slog.Level
	switch levelStr {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var writer io.Writer = os.Stdout

	if logFilePath != "" {
		// Ensure log dir exists
		dir := filepath.Dir(logFilePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}

		file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return nil, err
		}
		// Write to both stdout and log file
		writer = io.MultiWriter(os.Stdout, file)
	}

	handler := slog.NewTextHandler(writer, opts)
	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger, nil
}
