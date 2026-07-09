package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/config"
	"github.com/yumekaz/cairn/internal/duraflow"
	"github.com/yumekaz/cairn/internal/runtime"
	"github.com/yumekaz/cairn/internal/store"
	dfengine "github.com/yumekaz/duraflow/pkg/engine"
	dfexecutor "github.com/yumekaz/duraflow/pkg/executor"
	dfstore "github.com/yumekaz/duraflow/pkg/store"
	dfworker "github.com/yumekaz/duraflow/pkg/worker"
)

// Server coordinates the SQLite store, RuntimeBackend, API routing, and Unix socket lifecycle.
type Server struct {
	router    *chi.Mux
	store     *store.Store
	runtime   runtime.RuntimeBackend
	config    *config.DaemonConfig
	startTime time.Time
	duraflow  *duraflow.Engine
	dfWorker  *dfworker.WorkerDaemon
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

	srv.duraflow = duraflow.NewEngine(s, r)
	srv.RegisterDuraFlowTemplates(srv.duraflow)

	// Initialize real DuraFlow SQLite database
	dfDbPath := filepath.Join(cfg.DataDir, "duraflow.db")
	dfStore := dfstore.NewSQLiteStore(dfDbPath)
	if err := dfStore.Init(); err != nil {
		log.Fatalf("cairnd: Failed to initialize DuraFlow SQLite store: %v", err)
	}

	// Register executors
	dfExecReg := dfexecutor.NewRegistry()
	dfExecReg.Register("host", dfexecutor.NewHostExecutor())
	dfExecReg.Register("docker", dfexecutor.NewDockerExecutor())
	dfExecReg.Register("mini-docker", dfexecutor.NewMiniDockerExecutor())
	dfExecReg.Register("cairn", srv.duraflow)

	// Initialize real engine
	realDfEngine := dfengine.NewWorkflowEngine(dfStore, dfExecReg)
	srv.duraflow.SetRealEngine(realDfEngine)

	// Register event synchronization hook from DuraFlow to Cairn
	realDfEngine.OnEvent = srv.syncDuraFlowEventToCairn

	// Initialize real background worker
	srv.dfWorker = dfworker.NewWorkerDaemon(dfStore, realDfEngine)

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

	// Start background DuraFlow worker daemon
	if err := s.dfWorker.Start(); err != nil {
		log.Printf("cairnd: Warning: failed to start background worker: %v", err)
	}

	// Graceful worker shutdown
	go func() {
		<-ctx.Done()
		log.Println("cairnd: Stopping background worker daemon...")
		s.dfWorker.Stop()
	}()

	// Start periodic service state and runtime reconciliation loop
	go s.startReconciliationLoop(ctx)

	// Start periodic metadata auto-backup loop
	go s.startMetadataBackupLoop(ctx)

	// Start secondary TCP server for dashboard/API if configured
	if s.config.DashboardAddr != "" {
		tcpListener, err := net.Listen("tcp", s.config.DashboardAddr)
		if err == nil {
			log.Printf("cairnd: Dashboard and API TCP server listening on http://%s", s.config.DashboardAddr)
			httpServerTCP := &http.Server{
				Handler: s,
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

func (s *Server) startReconciliationLoop(ctx context.Context) {
	log.Println("cairnd: Starting service/runtime reconciliation loop...")
	// Run immediately on start
	s.ReconcileServices(ctx)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("cairnd: Stopping service/runtime reconciliation loop...")
			return
		case <-ticker.C:
			s.ReconcileServices(ctx)
		}
	}
}

func (s *Server) startMetadataBackupLoop(ctx context.Context) {
	log.Println("cairnd: Starting metadata auto-backup loop...")
	// Perform backup on start
	if err := s.BackupMetadata(); err != nil {
		log.Printf("cairnd: Initial metadata backup failed: %v", err)
	}

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("cairnd: Stopping metadata auto-backup loop...")
			return
		case <-ticker.C:
			if err := s.BackupMetadata(); err != nil {
				log.Printf("cairnd: Periodic metadata backup failed: %v", err)
			}
		}
	}
}

func (s *Server) BackupMetadata() error {
	backupDir := filepath.Join(s.config.DataDir, "backups", "metadata")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return err
	}

	timestamp := time.Now().Format("20060102_150405")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("cairn_%s.db", timestamp))

	src, err := os.Open(s.config.DatabasePath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(backupPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}

	// Prune old backups, keeping only the 5 most recent ones
	files, err := os.ReadDir(backupDir)
	if err == nil {
		var backupFiles []os.DirEntry
		for _, f := range files {
			if !f.IsDir() && strings.HasPrefix(f.Name(), "cairn_") && strings.HasSuffix(f.Name(), ".db") {
				backupFiles = append(backupFiles, f)
			}
		}
		if len(backupFiles) > 5 {
			sort.Slice(backupFiles, func(i, j int) bool {
				return backupFiles[i].Name() < backupFiles[j].Name()
			})
			for i := 0; i < len(backupFiles)-5; i++ {
				_ = os.Remove(filepath.Join(backupDir, backupFiles[i].Name()))
			}
		}
	}

	return nil
}

func (s *Server) ReconcileServices(ctx context.Context) {
	if s.runtime == nil {
		return
	}
	services, err := s.store.ListServices()
	if err != nil {
		log.Printf("cairnd: Failed to list services for reconciliation: %v\n", err)
		return
	}

	activeDeploys, _ := s.store.ListActiveDeployIDs()

	for _, svc := range services {
		// Skip reconciliation if the service is currently being deployed
		isActiveDeploy := false
		for _, depID := range activeDeploys {
			if svc.CurrentDeployID == depID {
				isActiveDeploy = true
				break
			}
		}
		if isActiveDeploy {
			continue
		}

		if svc.DesiredState == "running" {
			runStateMatches := false
			if svc.RuntimeID != "" {
				info, err := s.runtime.InspectContainer(ctx, svc.RuntimeID)
				if err == nil && info.State == runtime.StateRunning {
					runStateMatches = true
					if svc.ActualState != "running" {
						svc.ActualState = "running"
						_ = s.store.UpsertService(svc)
					}
				} else if err == nil {
					// Container exists but is not running (e.g. following host reboot or container crash)
					log.Printf("cairnd: Service %s container %s is stopped. Restarting...\n", svc.Name, svc.RuntimeID)
					if startErr := s.runtime.StartContainer(ctx, svc.RuntimeID); startErr == nil {
						svc.ActualState = "running"
						_ = s.store.UpsertService(svc)
						runStateMatches = true
					} else {
						log.Printf("cairnd: Failed to start container for service %s: %v\n", svc.Name, startErr)
					}
				}
			}

			if !runStateMatches {
				// Recreate container
				log.Printf("cairnd: Service %s container does not exist or failed. Recreating...\n", svc.Name)
				if svc.CurrentDeployID == "" {
					log.Printf("cairnd: Service %s has no current deployment configuration\n", svc.Name)
					continue
				}

				cfgPath := filepath.Join(s.config.DataDir, "services", svc.Name, fmt.Sprintf("deploy_%s.json", svc.CurrentDeployID))
				cfgJSON, err := os.ReadFile(cfgPath)
				if err != nil {
					log.Printf("cairnd: Failed to read deployment config for service %s: %v\n", svc.Name, err)
					continue
				}

				var cfg api.ServiceConfig
				if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
					log.Printf("cairnd: Failed to parse deployment config for service %s: %v\n", svc.Name, err)
					continue
				}

				candidateName := fmt.Sprintf("cairn-%s-%s", svc.Name, svc.CurrentDeployID[:8])
				cfg.Environment = s.resolveEnvPlaceholders(ctx, cfg.Environment)

				newID, err := s.runtime.CreateContainer(ctx, &cfg, candidateName)
				if err != nil {
					log.Printf("cairnd: Failed to recreate container for service %s: %v\n", svc.Name, err)
					continue
				}

				if err := s.runtime.StartContainer(ctx, newID); err != nil {
					log.Printf("cairnd: Failed to start recreated container for service %s: %v\n", svc.Name, err)
					_ = s.runtime.RemoveContainer(ctx, newID)
					continue
				}

				svc.RuntimeID = newID
				svc.ActualState = "running"
				_, err = s.runtime.InspectContainer(ctx, newID)
				if err == nil && len(cfg.Ports) > 0 {
					hostPort := cfg.Ports[0].Host
					if hostPort > 0 {
						svc.Route = fmt.Sprintf("http://localhost:%d", hostPort)
					}
				}
				_ = s.store.UpsertService(svc)
				log.Printf("cairnd: Service %s successfully recreated and started (ID: %s)\n", svc.Name, newID)
			}
		}
	}

	// 2. Mini-Docker runtime reconciliation: detect and clean up dangling containers
	runtimeContainers, err := s.runtime.ListContainers(ctx)
	if err == nil {
		for _, rc := range runtimeContainers {
			nameClean := strings.TrimPrefix(rc.Name, "/")
			if strings.HasPrefix(nameClean, "cairn-") && !strings.Contains(nameClean, "-restore-task-") && !strings.Contains(nameClean, "-backup-task-") {
				// Check if this container corresponds to a registered service's current running ID
				isRegistered := false
				for _, svc := range services {
					if svc.RuntimeID == rc.ID {
						isRegistered = true
						break
					}
				}
				if !isRegistered {
					// Check active deployments in SQLite to verify it's not a currently creating deployment candidate or migration task
					activeDeploys, err := s.store.ListActiveDeployIDs()
					isActiveDeploy := false
					if err == nil {
						for _, depID := range activeDeploys {
							if strings.Contains(nameClean, depID[:8]) {
								isActiveDeploy = true
								break
							}
						}
					}
					// Also fallback to check active workflows (like database backups/restores if not using deploys table)
					if !isActiveDeploy {
						deploys, err := s.store.ListRunningWorkflows()
						if err == nil {
							for _, w := range deploys {
								if strings.Contains(nameClean, w.ID[:8]) {
									isActiveDeploy = true
									break
								}
							}
						}
					}
					if !isActiveDeploy {
						log.Printf("cairnd: Corrupted state: found dangling untracked container %s (%s). Reconciling cleanup...\n", rc.Name, rc.ID)
						_ = s.runtime.StopContainer(ctx, rc.ID)
						_ = s.runtime.RemoveContainer(ctx, rc.ID)
					}
				}
			}
		}
	}
}

func (s *Server) syncDuraFlowEventToCairn(ev *dfstore.Event) {
	switch ev.EventType {
	case dfengine.EventWorkflowRunCreated:
		w := &api.Workflow{
			ID:               ev.RunID,
			Type:             ev.WorkflowName,
			Status:           "pending",
			CurrentStepIndex: 0,
			InputJSON:        s.duraflow.GetRunInput(ev.RunID),
		}
		_ = s.store.CreateWorkflow(w)

	case dfengine.EventWorkflowStarted:
		w, err := s.store.GetWorkflow(ev.RunID)
		if err == nil && w != nil {
			w.Status = "running"
			_ = s.store.UpdateWorkflow(w)
		}

	case dfengine.EventWorkflowCompleted:
		w, err := s.store.GetWorkflow(ev.RunID)
		if err == nil && w != nil {
			w.Status = "success"
			_ = s.store.UpdateWorkflow(w)
		}

	case dfengine.EventWorkflowFailed:
		w, err := s.store.GetWorkflow(ev.RunID)
		if err == nil && w != nil {
			w.Status = "failed"
			_ = s.store.UpdateWorkflow(w)
		}

	case dfengine.EventStepScheduled:
		// Find the index of the step in the template
		tmpl, ok := s.duraflow.GetTemplate(ev.WorkflowName)
		if ok {
			stepIdx := 0
			for idx, name := range tmpl.Steps {
				if name == ev.StepID {
					stepIdx = idx
					break
				}
			}
			step := &api.WorkflowStep{
				ID:         fmt.Sprintf("%s-%s", ev.RunID, ev.StepID),
				WorkflowID: ev.RunID,
				StepIndex:  stepIdx,
				Name:       ev.StepID,
				Status:     "pending",
			}
			_ = s.store.CreateWorkflowStep(step)
		}

	case dfengine.EventStepStarted:
		step := &api.WorkflowStep{
			ID:         fmt.Sprintf("%s-%s", ev.RunID, ev.StepID),
			WorkflowID: ev.RunID,
			Name:       ev.StepID,
			Status:     "running",
		}
		_ = s.store.UpdateWorkflowStep(step)

	case dfengine.EventStepSucceeded:
		step := &api.WorkflowStep{
			ID:         fmt.Sprintf("%s-%s", ev.RunID, ev.StepID),
			WorkflowID: ev.RunID,
			Name:       ev.StepID,
			Status:     "success",
		}
		_ = s.store.UpdateWorkflowStep(step)
		// Update current step index on the workflow run in Cairn
		tmpl, ok := s.duraflow.GetTemplate(ev.WorkflowName)
		if ok {
			stepIdx := 0
			for idx, name := range tmpl.Steps {
				if name == ev.StepID {
					stepIdx = idx
					break
				}
			}
			w, err := s.store.GetWorkflow(ev.RunID)
			if err == nil && w != nil {
				w.CurrentStepIndex = stepIdx + 1
				_ = s.store.UpdateWorkflow(w)
			}
		}

	case dfengine.EventStepFailedFinal, dfengine.EventStepTimedOut:
		// Extract error from payload if possible
		var p struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal([]byte(ev.PayloadJSON), &p)
		errMsg := p.Error
		if errMsg == "" {
			errMsg = "step failed"
		}
		step := &api.WorkflowStep{
			ID:           fmt.Sprintf("%s-%s", ev.RunID, ev.StepID),
			WorkflowID:   ev.RunID,
			Name:         ev.StepID,
			Status:       "failed",
			ErrorMessage: errMsg,
		}
		_ = s.store.UpdateWorkflowStep(step)
	}
}
