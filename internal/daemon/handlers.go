package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/events"
	"github.com/yumekaz/cairn/internal/store"
)

func (s *Server) setupRoutes() {
	s.router.Get("/status", s.handleStatus)

	s.router.Route("/services", func(r chi.Router) {
		r.Get("/", s.handleListServices)
		r.Post("/", s.handleCreateService)
		r.Route("/{name}", func(r chi.Router) {
			r.Get("/", s.handleGetService)
			r.Post("/start", s.handleStartService)
			r.Post("/stop", s.handleStopService)
			r.Post("/restart", s.handleRestartService)
			r.Delete("/", s.handleRemoveService)
		})
	})

	s.router.Get("/events", s.handleListEvents)
}

func (s *Server) json(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) error(w http.ResponseWriter, status int, message string) {
	s.json(w, status, map[string]string{"error": message})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	services, err := s.store.ListServices()
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	activeCount := 0
	for _, svc := range services {
		if svc.ActualState == "running" {
			activeCount++
		}
	}

	status := api.DaemonStatus{
		Uptime:         time.Since(s.startTime).Truncate(time.Second).String(),
		Version:        "0.1.0",
		ActiveServices: activeCount,
		StorageUsage:   "N/A",
	}

	s.json(w, http.StatusOK, status)
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	services, err := s.store.ListServices()
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.json(w, http.StatusOK, services)
}

func (s *Server) handleGetService(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	svc, err := s.store.GetServiceByName(name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if svc == nil {
		s.error(w, http.StatusNotFound, "service not found")
		return
	}
	s.json(w, http.StatusOK, svc)
}

func (s *Server) handleCreateService(w http.ResponseWriter, r *http.Request) {
	var cfg api.ServiceConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		s.error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if cfg.Name == "" {
		s.error(w, http.StatusBadRequest, "service name is required")
		return
	}

	// 1. Get or create service record
	existing, err := s.store.GetServiceByName(cfg.Name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	var svc *api.Service
	var previousRuntimeID string
	if existing != nil {
		svc = existing
		previousRuntimeID = svc.RuntimeID
	} else {
		svc = &api.Service{
			ID:             uuid.New().String(),
			Name:           cfg.Name,
			Kind:           cfg.Kind,
			RuntimeBackend: "mini-docker",
			DesiredState:   "stopped",
			ActualState:    "stopped",
		}
	}

	deployID := uuid.New().String()
	deploy := &api.Deploy{
		ID:           deployID,
		ServiceID:    svc.ID,
		Version:      "v1",
		SourcePath:   "inline",
		Status:       "pending",
		Stage:        "starting",
		HealthStatus: "unhealthy",
	}

	// First upsert the service to satisfy the foreign key constraint in the deploys table
	if err := s.store.UpsertService(svc); err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := s.store.CreateDeploy(deploy); err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Now update the service with the active deploy ID
	svc.CurrentDeployID = deployID
	if err := s.store.UpsertService(svc); err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Record start event
	s.store.CreateEvent(&api.Event{
		ID:        uuid.New().String(),
		ServiceID: &svc.ID,
		DeployID:  &deployID,
		Type:      events.DeployStarted.String(),
		Message:   fmt.Sprintf("Deploying service %s (Deploy ID: %s)", svc.Name, deployID),
	})

	// 2. Perform actual container orchestration
	ctx := r.Context()
	candidateName := fmt.Sprintf("cairn-%s-%s", svc.Name, deployID[:8])

	// Create candidate container in Mini-Docker
	candidateID, err := s.runtime.CreateContainer(ctx, &cfg, candidateName)
	if err != nil {
		s.failDeploy(deploy, svc, "Failed to create container: "+err.Error())
		s.error(w, http.StatusInternalServerError, "Deployment failed: "+err.Error())
		return
	}

	// Start candidate container
	if err := s.runtime.StartContainer(ctx, candidateID); err != nil {
		s.runtime.RemoveContainer(ctx, candidateID) // clean up candidate
		s.failDeploy(deploy, svc, "Failed to start container: "+err.Error())
		s.error(w, http.StatusInternalServerError, "Deployment failed: "+err.Error())
		return
	}

	// Find mapped host port to run health checks on
	hostPort := 0
	if len(cfg.Ports) > 0 {
		hostPort = cfg.Ports[0].Host
	}

	// Run health check if configured and ports exist
	if hostPort > 0 && cfg.HealthCheck != nil {
		info, err := s.runtime.InspectContainer(ctx, candidateID)
		if err != nil {
			s.failDeploy(deploy, svc, "Failed to inspect container: "+err.Error())
			s.error(w, http.StatusInternalServerError, "Deployment failed: "+err.Error())
			return
		}
		if err := RunHealthCheck(ctx, cfg.HealthCheck, info.IPAddress, cfg.Ports[0].Container); err != nil {
			// Clean up candidate
			s.runtime.StopContainer(ctx, candidateID)
			s.runtime.RemoveContainer(ctx, candidateID)

			s.failDeploy(deploy, svc, "Health check failed: "+err.Error())
			s.error(w, http.StatusInternalServerError, "Deployment failed: container health checks failed: "+err.Error())
			return
		}
	}

	// Success! Route traffic and clean up the old container
	if previousRuntimeID != "" {
		s.runtime.StopContainer(ctx, previousRuntimeID)
		s.runtime.RemoveContainer(ctx, previousRuntimeID)
	}

	// Update DB status
	deploy.Status = "success"
	deploy.Stage = "completed"
	deploy.HealthStatus = "healthy"
	timeNow := time.Now()
	deploy.CompletedAt = &timeNow
	s.store.UpdateDeploy(deploy)

	svc.RuntimeID = candidateID
	svc.DesiredState = "running"
	svc.ActualState = "running"
	if hostPort > 0 {
		svc.Route = fmt.Sprintf("http://localhost:%d", hostPort)
	} else {
		svc.Route = "N/A"
	}
	s.store.UpsertService(svc)

	s.store.CreateEvent(&api.Event{
		ID:        uuid.New().String(),
		ServiceID: &svc.ID,
		DeployID:  &deployID,
		Type:      events.DeploySucceeded.String(),
		Message:   fmt.Sprintf("Successfully deployed service %s (container: %s)", svc.Name, candidateName),
	})

	s.json(w, http.StatusCreated, svc)
}

func (s *Server) failDeploy(deploy *api.Deploy, svc *api.Service, reason string) {
	deploy.Status = "failed"
	deploy.Stage = "completed"
	deploy.FailureReason = reason
	timeNow := time.Now()
	deploy.CompletedAt = &timeNow
	s.store.UpdateDeploy(deploy)

	s.store.CreateEvent(&api.Event{
		ID:        uuid.New().String(),
		ServiceID: &svc.ID,
		DeployID:  &deploy.ID,
		Type:      events.DeployFailed.String(),
		Message:   fmt.Sprintf("Deployment failed: %s", reason),
	})
}

func (s *Server) handleStartService(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	svc, err := s.store.GetServiceByName(name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if svc == nil {
		s.error(w, http.StatusNotFound, "service not found")
		return
	}

	if svc.RuntimeID != "" {
		if err := s.runtime.StartContainer(r.Context(), svc.RuntimeID); err != nil {
			s.error(w, http.StatusInternalServerError, "failed to start container: "+err.Error())
			return
		}
	} else {
		s.error(w, http.StatusBadRequest, "service has no deployed container")
		return
	}

	svc.DesiredState = "running"
	svc.ActualState = "running"
	if err := s.store.UpsertService(svc); err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.store.CreateEvent(&api.Event{
		ID:        uuid.New().String(),
		ServiceID: &svc.ID,
		Type:      events.ServiceStarted.String(),
		Message:   "Started service " + svc.Name,
	})

	s.json(w, http.StatusOK, svc)
}

func (s *Server) handleStopService(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	svc, err := s.store.GetServiceByName(name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if svc == nil {
		s.error(w, http.StatusNotFound, "service not found")
		return
	}

	if svc.RuntimeID != "" {
		if err := s.runtime.StopContainer(r.Context(), svc.RuntimeID); err != nil {
			s.error(w, http.StatusInternalServerError, "failed to stop container: "+err.Error())
			return
		}
	}

	svc.DesiredState = "stopped"
	svc.ActualState = "stopped"
	if err := s.store.UpsertService(svc); err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.store.CreateEvent(&api.Event{
		ID:        uuid.New().String(),
		ServiceID: &svc.ID,
		Type:      events.ServiceStopped.String(),
		Message:   "Stopped service " + svc.Name,
	})

	s.json(w, http.StatusOK, svc)
}

func (s *Server) handleRestartService(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	svc, err := s.store.GetServiceByName(name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if svc == nil {
		s.error(w, http.StatusNotFound, "service not found")
		return
	}

	if svc.RuntimeID != "" {
		if err := s.runtime.RestartContainer(r.Context(), svc.RuntimeID); err != nil {
			s.error(w, http.StatusInternalServerError, "failed to restart container: "+err.Error())
			return
		}
	} else {
		s.error(w, http.StatusBadRequest, "service has no deployed container")
		return
	}

	svc.DesiredState = "running"
	svc.ActualState = "running"
	if err := s.store.UpsertService(svc); err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.store.CreateEvent(&api.Event{
		ID:        uuid.New().String(),
		ServiceID: &svc.ID,
		Type:      events.ServiceRestarted.String(),
		Message:   "Restarted service " + svc.Name,
	})

	s.json(w, http.StatusOK, svc)
}

func (s *Server) handleRemoveService(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	svc, err := s.store.GetServiceByName(name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if svc == nil {
		s.error(w, http.StatusNotFound, "service not found")
		return
	}

	if svc.RuntimeID != "" {
		if err := s.runtime.RemoveContainer(r.Context(), svc.RuntimeID); err != nil {
			// Don't block delete if container was already cleaned up
			s.store.CreateEvent(&api.Event{
				ID:        uuid.New().String(),
				ServiceID: &svc.ID,
				Type:      events.ServiceRemoved.String(),
				Message:   fmt.Sprintf("Warning: failed to remove runtime container: %v", err),
			})
		}
	}

	if err := s.store.DeleteService(svc.ID); err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.store.CreateEvent(&api.Event{
		ID:        uuid.New().String(),
		ServiceID: &svc.ID,
		Type:      events.ServiceRemoved.String(),
		Message:   "Removed service " + svc.Name,
	})

	s.json(w, http.StatusOK, map[string]string{"message": "service removed"})
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	evts, err := s.store.ListEvents(store.EventFilter{})
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.json(w, http.StatusOK, evts)
}
