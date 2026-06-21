package logging

import (
	"io"
	"log/slog"
	"os"
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
		// Use a rotatable file writer (10MB max size, 3 backup files)
		file := NewRotatableFileWriter(logFilePath, 10*1024*1024, 3)
		// Write to both stdout and log file
		writer = io.MultiWriter(os.Stdout, file)
	}

	handler := slog.NewTextHandler(writer, opts)
	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger, nil
}
