package daemon

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/yumekaz/cairn/internal/config"
	"github.com/yumekaz/cairn/internal/runtime"
	"github.com/yumekaz/cairn/internal/store"
)

// Server coordinates the SQLite store, RuntimeBackend, API routing, and Unix socket lifecycle.
type Server struct {
	router    *chi.Mux
	store     *store.Store
	runtime   runtime.RuntimeBackend
	config    *config.DaemonConfig
	startTime time.Time
}

// NewServer initializes the Daemon API Server.
func NewServer(cfg *config.DaemonConfig, s *store.Store, r runtime.RuntimeBackend) *Server {
	srv := &Server{
		router:    chi.NewRouter(),
		store:     s,
		runtime:   r,
		config:    cfg,
		startTime: time.Now(),
	}

	srv.setupMiddleware()
	srv.setupRoutes()
	return srv
}

func (s *Server) setupMiddleware() {
	s.router.Use(middleware.RequestID)
	s.router.Use(middleware.RealIP)
	s.router.Use(middleware.Logger)
	s.router.Use(middleware.Recoverer)
}

// Start listens on the configured Unix socket and optionally on TCP.
func (s *Server) Start(ctx context.Context) error {
	socketPath := s.config.SocketPath

	// Ensure directory for socket exists
	socketDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return err
	}

	// Remove old socket if exists
	if _, err := os.Stat(socketPath); err == nil {
		if err := os.Remove(socketPath); err != nil {
			return err
		}
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}

	// Make sure the socket file is cleaned up on close
	defer os.Remove(socketPath)

	// Start background cron scheduler
	sched := NewScheduler(s.store, s.runtime, s.config.DataDir)
	go sched.Start(ctx)

	// Start secondary TCP server for dashboard/API if configured
	if s.config.DashboardAddr != "" {
		tcpListener, err := net.Listen("tcp", s.config.DashboardAddr)
		if err == nil {
			log.Printf("cairnd: Dashboard and API TCP server listening on http://%s", s.config.DashboardAddr)
			httpServerTCP := &http.Server{
				Handler: s.router,
			}
			go func() {
				if err := httpServerTCP.Serve(tcpListener); err != nil && err != http.ErrServerClosed {
					log.Printf("cairnd: Dashboard TCP server error: %v", err)
				}
			}()
			// Graceful shutdown for TCP server
			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				httpServerTCP.Shutdown(shutdownCtx)
			}()
		} else {
			log.Printf("cairnd: Warning: Failed to start dashboard TCP server on %s: %v", s.config.DashboardAddr, err)
		}
	}

	httpServer := &http.Server{
		Handler: s.router,
	}

	// Graceful shutdown goroutine for primary Unix server
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdownCtx)
	}()

	return httpServer.Serve(listener)
}
