package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/config"
	"github.com/yumekaz/cairn/internal/daemon"
	"github.com/yumekaz/cairn/internal/events"
	"github.com/yumekaz/cairn/internal/logging"
	"github.com/yumekaz/cairn/internal/runtime/minidocker"
	"github.com/yumekaz/cairn/internal/store"
)

func main() {
	configPath := flag.String("config", "", "Path to cairnd configuration file")
	logLevel := flag.String("log-level", "info", "Logging level (debug, info, warn, error)")
	flag.Parse()

	// 1. Load config
	cfg, err := config.LoadDaemonConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load daemon config: %v", err)
	}

	// 2. Setup logging
	logFile := filepath.Join(cfg.DataDir, "cairnd.log")
	logger, err := logging.SetupLogger(*logLevel, logFile)
	if err != nil {
		log.Fatalf("Failed to setup logger: %v", err)
	}

	logger.Info("Starting Cairn daemon", "version", "0.1.0", "socket", cfg.SocketPath, "db", cfg.DatabasePath)

	// 3. Acquire PID file
	pidFile := filepath.Join(cfg.DataDir, "cairnd.pid")
	pf := daemon.NewPIDFile(pidFile)
	if err := pf.Acquire(); err != nil {
		logger.Error("Failed to acquire PID file", "path", pidFile, "error", err)
		os.Exit(1)
	}
	defer pf.Release()

	// 4. Initialize SQLite Store
	dbStore, err := store.NewStore(cfg.DatabasePath)
	if err != nil {
		logger.Error("Failed to initialize SQLite store", "error", err)
		os.Exit(1)
	}
	defer dbStore.Close()

	// 5. Initialize Runtime Backend (Mini-Docker)
	rtBackend := minidocker.NewAdapter(cfg.MiniDockerSocket, cfg.VolumeDir)

	// 6. Setup Signal Handlers
	ctx, cancel := daemon.SetupSignalContext()
	defer cancel()

	// 7. Record Daemon Started event
	dbStore.CreateEvent(&api.Event{
		ID:        uuid.New().String(),
		Type:      events.DaemonStarted.String(),
		Message:   "Cairn daemon started successfully",
		CreatedAt: time.Now(),
	})

	// 8. Initialize & Start API Server
	srv := daemon.NewServer(cfg, dbStore, rtBackend)

	logger.Info("Daemon listening on Unix socket", "path", cfg.SocketPath)
	if err := srv.Start(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("Daemon server stopped with error", "error", err)
		os.Exit(1)
	}

	// 9. Record Daemon Stopped event
	dbStore.CreateEvent(&api.Event{
		ID:      uuid.New().String(),
		Type:    events.DaemonStopped.String(),
		Message: "Cairn daemon stopped",
	})

	logger.Info("Cairn daemon shutdown cleanly")
}
