package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/events"
	"github.com/yumekaz/cairn/internal/runtime"
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
			r.Get("/logs", s.handleServiceLogs)
			r.Post("/rollback", s.handleRollbackService)
		})
	})

	s.router.Route("/volumes", func(r chi.Router) {
		r.Get("/", s.handleListVolumes)
		r.Post("/", s.handleCreateVolume)
		r.Route("/{name}", func(r chi.Router) {
			r.Get("/", s.handleGetVolume)
			r.Post("/backups", s.handleCreateBackup)
			r.Get("/backups", s.handleListBackups)
			r.Post("/restore", s.handleRestoreBackup)
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

	var totalBytes int64
	if s.config.VolumeDir != "" {
		totalBytes += getDirSize(s.config.VolumeDir)
	}
	if s.config.BackupDir != "" {
		totalBytes += getDirSize(s.config.BackupDir)
	}

	status := api.DaemonStatus{
		Uptime:         time.Since(s.startTime).Truncate(time.Second).String(),
		Version:        "0.1.0",
		ActiveServices: activeCount,
		StorageUsage:   formatSize(totalBytes),
	}

	s.json(w, http.StatusOK, status)
}

func getDirSize(path string) int64 {
	var size int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
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

	// Ensure and register volumes in SQLite
	for _, volConfig := range cfg.Volumes {
		existingVol, err := s.store.GetVolumeByName(volConfig.Name)
		if err != nil {
			s.error(w, http.StatusInternalServerError, err.Error())
			return
		}
		if existingVol == nil {
			vol := &api.Volume{
				ID:                uuid.New().String(),
				Name:              volConfig.Name,
				HostPath:          filepath.Join(s.config.VolumeDir, volConfig.Name),
				AttachedServiceID: svc.ID,
				MountPath:         volConfig.MountPath,
				Status:            "active",
				CreatedAt:         time.Now(),
				UpdatedAt:         time.Now(),
			}
			if err := s.store.UpsertVolume(vol); err != nil {
				s.error(w, http.StatusInternalServerError, err.Error())
				return
			}
		} else {
			existingVol.AttachedServiceID = svc.ID
			existingVol.MountPath = volConfig.MountPath
			existingVol.UpdatedAt = time.Now()
			if err := s.store.UpsertVolume(existingVol); err != nil {
				s.error(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}

	// Record start event
	s.store.CreateEvent(&api.Event{
		ID:        uuid.New().String(),
		ServiceID: &svc.ID,
		DeployID:  &deployID,
		Type:      events.DeployStarted.String(),
		Message:   fmt.Sprintf("Deploying service %s (Deploy ID: %s)", svc.Name, deployID),
	})

	// Run flow
	updatedSvc, err := s.runDeployFlow(r.Context(), &cfg, deploy, svc, previousRuntimeID)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "Deployment failed: "+err.Error())
		return
	}

	s.json(w, http.StatusCreated, updatedSvc)
}

func (s *Server) runDeployFlow(ctx context.Context, cfg *api.ServiceConfig, deploy *api.Deploy, svc *api.Service, previousRuntimeID string) (*api.Service, error) {
	// 1. Save configuration JSON
	svcDir := filepath.Join(s.config.DataDir, "services", cfg.Name)
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		s.failDeploy(deploy, svc, "Failed to create service config directory: "+err.Error())
		return nil, err
	}
	cfgPath := filepath.Join(svcDir, fmt.Sprintf("deploy_%s.json", deploy.ID))
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		s.failDeploy(deploy, svc, "Failed to serialize service config: "+err.Error())
		return nil, err
	}
	if err := os.WriteFile(cfgPath, cfgJSON, 0644); err != nil {
		s.failDeploy(deploy, svc, "Failed to write service config: "+err.Error())
		return nil, err
	}

	// 2. Pre-deploy backup gate if migration is non-empty
	if cfg.Migration != "" {
		for _, volConfig := range cfg.Volumes {
			vol, err := s.store.GetVolumeByName(volConfig.Name)
			if err != nil {
				s.failDeploy(deploy, svc, "Failed to retrieve volume for backup: "+err.Error())
				return nil, err
			}
			if vol != nil {
				backup, err := s.performVolumeBackup(vol)
				if err != nil {
					s.failDeploy(deploy, svc, fmt.Sprintf("Pre-deploy backup failed for volume %s: %s", vol.Name, err.Error()))
					return nil, err
				}
				if backup.Status == "failed" {
					reason := backup.FailureReason
					if reason == "" {
						reason = "unknown error"
					}
					s.failDeploy(deploy, svc, fmt.Sprintf("Pre-deploy backup failed for volume %s: %s", vol.Name, reason))
					return nil, fmt.Errorf("pre-deploy backup failed: %s", reason)
				}
			}
		}

		// 3. Execute migration container
		taskName := fmt.Sprintf("cairn-%s-task-%s", svc.Name, deploy.ID[:8])
		taskCfg := &api.ServiceConfig{
			Name:        cfg.Name,
			Kind:        cfg.Kind,
			Image:       cfg.Image,
			Command:     []string{"/bin/sh", "-c", cfg.Migration},
			Environment: cfg.Environment,
			Volumes:     cfg.Volumes,
		}

		taskID, err := s.runtime.CreateContainer(ctx, taskCfg, taskName)
		if err != nil {
			s.failDeploy(deploy, svc, "Failed to create migration task container: "+err.Error())
			return nil, err
		}

		// Start migration task container
		if err := s.runtime.StartContainer(ctx, taskID); err != nil {
			s.runtime.RemoveContainer(ctx, taskID)
			s.failDeploy(deploy, svc, "Failed to start migration task container: "+err.Error())
			return nil, err
		}

		// Wait for migration task container to exit
		var exitCode int
		var runErr error
		for {
			info, err := s.runtime.InspectContainer(ctx, taskID)
			if err != nil {
				runErr = err
				break
			}
			if info.State == runtime.StateStopped || info.State == runtime.StateError {
				if info.ExitCode != nil {
					exitCode = *info.ExitCode
				} else {
					exitCode = -1
				}
				break
			}
			select {
			case <-ctx.Done():
				runErr = ctx.Err()
				break
			case <-time.After(200 * time.Millisecond):
			}
		}

		if runErr != nil {
			s.runtime.RemoveContainer(ctx, taskID)
			s.failDeploy(deploy, svc, "Migration execution context failed: "+runErr.Error())
			return nil, runErr
		}

		if exitCode != 0 {
			logs := s.getContainerLogs(ctx, taskID)
			s.runtime.RemoveContainer(ctx, taskID)
			s.failDeploy(deploy, svc, fmt.Sprintf("Migration failed with exit code %d. Logs:\n%s", exitCode, logs))
			return nil, fmt.Errorf("migration failed (exit code %d): %s", exitCode, logs)
		}

		// Clean up migration container
		s.runtime.RemoveContainer(ctx, taskID)

		// Wait a moment for network and ARP tables to settle
		time.Sleep(1 * time.Second)

		// Set StateTouched = true
		deploy.StateTouched = true
		if err := s.store.UpdateDeploy(deploy); err != nil {
			return nil, err
		}
	}

	// 4. Perform actual container orchestration
	candidateName := fmt.Sprintf("cairn-%s-%s", svc.Name, deploy.ID[:8])

	// Create candidate container in Mini-Docker
	candidateID, err := s.runtime.CreateContainer(ctx, cfg, candidateName)
	if err != nil {
		s.failDeploy(deploy, svc, "Failed to create container: "+err.Error())
		return nil, err
	}

	// Start candidate container
	if err := s.runtime.StartContainer(ctx, candidateID); err != nil {
		s.runtime.RemoveContainer(ctx, candidateID) // clean up candidate
		s.failDeploy(deploy, svc, "Failed to start container: "+err.Error())
		return nil, err
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
			return nil, err
		}
		if err := RunHealthCheck(ctx, cfg.HealthCheck, info.IPAddress, cfg.Ports[0].Container); err != nil {
			// Fetch candidate container logs before cleaning it up
			logs := s.getContainerLogs(ctx, candidateID)

			// Clean up candidate
			s.runtime.StopContainer(ctx, candidateID)
			s.runtime.RemoveContainer(ctx, candidateID)

			s.failDeploy(deploy, svc, fmt.Sprintf("Health check failed: %s. Logs:\n%s", err.Error(), logs))
			return nil, fmt.Errorf("container health checks failed: %w", err)
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
		DeployID:  &deploy.ID,
		Type:      events.DeploySucceeded.String(),
		Message:   fmt.Sprintf("Successfully deployed service %s (container: %s)", svc.Name, candidateName),
	})

	return svc, nil
}

func (s *Server) getContainerLogs(ctx context.Context, id string) string {
	stream, err := s.runtime.StreamLogs(ctx, id, false, 100)
	if err != nil {
		return "failed to get logs: " + err.Error()
	}
	defer stream.Close()

	bytes, err := io.ReadAll(stream)
	if err != nil {
		return "failed to read logs: " + err.Error()
	}
	return string(bytes)
}

func (s *Server) performVolumeBackup(vol *api.Volume) (*api.Backup, error) {
	// Ensure the volume host directory exists before performing backup
	if err := os.MkdirAll(vol.HostPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create host volume directory: %w", err)
	}

	backupID := fmt.Sprintf("backup_%s_%s", vol.Name, time.Now().Format("20060102_150405"))
	backupDir := filepath.Join(s.config.BackupDir, vol.Name)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create backup directory: %w", err)
	}

	backupPath := filepath.Join(backupDir, backupID+".tar.gz")

	b := &api.Backup{
		ID:         backupID,
		VolumeID:   vol.ID,
		BackupPath: backupPath,
		Status:     "pending",
		CreatedAt:  time.Now(),
	}

	if err := s.store.CreateBackup(b); err != nil {
		return nil, fmt.Errorf("failed to create backup record: %w", err)
	}

	s.store.CreateEvent(&api.Event{
		ID:       uuid.New().String(),
		VolumeID: &vol.ID,
		BackupID: &b.ID,
		Type:     events.BackupStarted.String(),
		Message:  fmt.Sprintf("Started backup for volume %s (ID: %s)", vol.Name, backupID),
	})

	checksum, sizeBytes, err := CreateTarGz(vol.HostPath, backupPath)
	if err != nil {
		b.Status = "failed"
		b.FailureReason = err.Error()
		timeNow := time.Now()
		b.CompletedAt = &timeNow
		s.store.UpdateBackup(b)

		s.store.CreateEvent(&api.Event{
			ID:       uuid.New().String(),
			VolumeID: &vol.ID,
			BackupID: &b.ID,
			Type:     events.BackupFailed.String(),
			Message:  fmt.Sprintf("Backup failed for volume %s: %v", vol.Name, err),
		})

		return b, fmt.Errorf("failed to create tarball: %w", err)
	}

	b.Status = "success"
	b.SizeBytes = sizeBytes
	b.Checksum = checksum
	timeNow := time.Now()
	b.CompletedAt = &timeNow
	if err := s.store.UpdateBackup(b); err != nil {
		return b, fmt.Errorf("failed to update backup record: %w", err)
	}

	s.store.CreateEvent(&api.Event{
		ID:       uuid.New().String(),
		VolumeID: &vol.ID,
		BackupID: &b.ID,
		Type:     events.BackupSucceeded.String(),
		Message:  fmt.Sprintf("Backup completed successfully for volume %s (Size: %d bytes)", vol.Name, sizeBytes),
	})

	return b, nil
}

func (s *Server) handleRollbackService(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	var req struct {
		DeployID string `json:"deploy_id"`
		Force    bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.DeployID == "" {
		s.error(w, http.StatusBadRequest, "deploy_id is required")
		return
	}

	svc, err := s.store.GetServiceByName(name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if svc == nil {
		s.error(w, http.StatusNotFound, "service not found")
		return
	}

	// Fetch all deploys for the service
	deploys, err := s.store.ListDeploys(svc.ID)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Find target deploy
	var targetDeploy *api.Deploy
	for _, d := range deploys {
		if d.ID == req.DeployID {
			targetDeploy = d
			break
		}
	}

	if targetDeploy == nil {
		s.error(w, http.StatusNotFound, "target deployment not found")
		return
	}

	// Check if target deploy is already the current deploy
	if svc.CurrentDeployID == targetDeploy.ID {
		s.error(w, http.StatusBadRequest, "service is already at target deployment")
		return
	}

	// Find intervening deploys with state_touched = true
	var dangerousDeploys []*api.Deploy
	for _, d := range deploys {
		if d.Status == "success" && d.StateTouched && d.CreatedAt.After(targetDeploy.CreatedAt) {
			dangerousDeploys = append(dangerousDeploys, d)
		}
	}

	if len(dangerousDeploys) > 0 && !req.Force {
		msg := fmt.Sprintf("Rollback target '%s' is unsafe: %d intervening successful deployment(s) executed migrations and modified state since then (including deploy '%s'). Proceeding might cause data or schema mismatch.",
			targetDeploy.ID[:8], len(dangerousDeploys), dangerousDeploys[0].ID[:8])
		s.error(w, http.StatusConflict, msg)
		return
	}

	// Load target deploy config from disk
	cfgPath := filepath.Join(s.config.DataDir, "services", svc.Name, fmt.Sprintf("deploy_%s.json", targetDeploy.ID))
	cfgJSON, err := os.ReadFile(cfgPath)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "failed to read target deploy configuration: "+err.Error())
		return
	}

	var cfg api.ServiceConfig
	if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
		s.error(w, http.StatusInternalServerError, "failed to parse target deploy configuration: "+err.Error())
		return
	}

	// Create a new Deploy record for the rollback
	newDeployID := uuid.New().String()
	newDeploy := &api.Deploy{
		ID:               newDeployID,
		ServiceID:        svc.ID,
		Version:          targetDeploy.Version,
		SourcePath:       "rollback",
		Status:           "pending",
		Stage:            "starting",
		HealthStatus:     "unhealthy",
		PreviousDeployID: svc.CurrentDeployID,
	}

	if err := s.store.CreateDeploy(newDeploy); err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Record start event
	s.store.CreateEvent(&api.Event{
		ID:        uuid.New().String(),
		ServiceID: &svc.ID,
		DeployID:  &newDeployID,
		Type:      events.DeployStarted.String(),
		Message:   fmt.Sprintf("Rolling back service %s to Deploy ID: %s (New Deploy ID: %s)", svc.Name, targetDeploy.ID, newDeployID),
	})

	previousRuntimeID := svc.RuntimeID

	// Run flow
	updatedSvc, err := s.runDeployFlow(r.Context(), &cfg, newDeploy, svc, previousRuntimeID)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "Rollback failed: "+err.Error())
		return
	}

	s.json(w, http.StatusOK, updatedSvc)
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

func (s *Server) handleCreateVolume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		MountPath string `json:"mount_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		s.error(w, http.StatusBadRequest, "volume name is required")
		return
	}

	existing, err := s.store.GetVolumeByName(req.Name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing != nil {
		s.error(w, http.StatusConflict, "volume already exists")
		return
	}

	hostPath := filepath.Join(s.config.VolumeDir, req.Name)
	if err := os.MkdirAll(hostPath, 0755); err != nil {
		s.error(w, http.StatusInternalServerError, "failed to create host directory: "+err.Error())
		return
	}

	vol := &api.Volume{
		ID:        uuid.New().String(),
		Name:      req.Name,
		HostPath:  hostPath,
		MountPath: req.MountPath,
		Status:    "active",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.store.UpsertVolume(vol); err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.store.CreateEvent(&api.Event{
		ID:       uuid.New().String(),
		VolumeID: &vol.ID,
		Type:     events.VolumeCreated.String(),
		Message:  fmt.Sprintf("Created volume %s", vol.Name),
	})

	s.json(w, http.StatusCreated, vol)
}

func (s *Server) handleListVolumes(w http.ResponseWriter, r *http.Request) {
	vols, err := s.store.ListVolumes()
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.json(w, http.StatusOK, vols)
}

func (s *Server) handleGetVolume(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	vol, err := s.store.GetVolumeByName(name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if vol == nil {
		s.error(w, http.StatusNotFound, "volume not found")
		return
	}
	s.json(w, http.StatusOK, vol)
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	vol, err := s.store.GetVolumeByName(name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if vol == nil {
		s.error(w, http.StatusNotFound, "volume not found")
		return
	}

	b, err := s.performVolumeBackup(vol)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.json(w, http.StatusCreated, b)
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	vol, err := s.store.GetVolumeByName(name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if vol == nil {
		s.error(w, http.StatusNotFound, "volume not found")
		return
	}

	backups, err := s.store.ListBackups(vol.ID)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.json(w, http.StatusOK, backups)
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var req struct {
		BackupID string `json:"backup_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	vol, err := s.store.GetVolumeByName(name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if vol == nil {
		s.error(w, http.StatusNotFound, "volume not found")
		return
	}

	backup, err := s.store.GetBackup(req.BackupID)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if backup == nil || backup.VolumeID != vol.ID {
		s.error(w, http.StatusBadRequest, "backup does not exist or does not belong to this volume")
		return
	}

	s.store.CreateEvent(&api.Event{
		ID:       uuid.New().String(),
		VolumeID: &vol.ID,
		BackupID: &backup.ID,
		Type:     events.BackupStarted.String(),
		Message:  fmt.Sprintf("Restoring volume %s from backup %s", vol.Name, backup.ID),
	})

	// Check if attached service is running and needs to be stopped
	var serviceRunning bool
	var service *api.Service
	if vol.AttachedServiceID != "" {
		service, err = s.store.GetService(vol.AttachedServiceID)
		if err != nil {
			s.error(w, http.StatusInternalServerError, err.Error())
			return
		}
		if service != nil && service.ActualState == "running" && service.RuntimeID != "" {
			serviceRunning = true
			// Stop service
			if err := s.runtime.StopContainer(r.Context(), service.RuntimeID); err != nil {
				s.error(w, http.StatusInternalServerError, "failed to stop service before restore: "+err.Error())
				return
			}
		}
	}

	// Safety: move current host path to temp safety location
	tempPath := vol.HostPath + ".tmp_restore_bak"
	if _, err := os.Stat(vol.HostPath); err == nil {
		if err := os.Rename(vol.HostPath, tempPath); err != nil {
			s.error(w, http.StatusInternalServerError, "failed to move volume to safety directory: "+err.Error())
			// Restart service if it was stopped
			if serviceRunning {
				s.runtime.StartContainer(r.Context(), service.RuntimeID)
			}
			return
		}
	}

	// Verify checksum of backup archive
	calcSum, err := ComputeFileSha256(backup.BackupPath)
	if err != nil {
		s.restoreRollback(vol.HostPath, tempPath)
		if serviceRunning {
			s.runtime.StartContainer(r.Context(), service.RuntimeID)
		}
		s.error(w, http.StatusInternalServerError, "failed to calculate backup checksum: "+err.Error())
		return
	}

	if calcSum != backup.Checksum {
		s.restoreRollback(vol.HostPath, tempPath)
		if serviceRunning {
			s.runtime.StartContainer(r.Context(), service.RuntimeID)
		}
		s.error(w, http.StatusBadRequest, fmt.Sprintf("backup checksum mismatch: expected %s, got %s", backup.Checksum, calcSum))
		return
	}

	// Extract backup tarball
	if err := ExtractTarGz(backup.BackupPath, vol.HostPath); err != nil {
		s.restoreRollback(vol.HostPath, tempPath)
		if serviceRunning {
			s.runtime.StartContainer(r.Context(), service.RuntimeID)
		}
		s.error(w, http.StatusInternalServerError, "failed to extract backup tarball: "+err.Error())
		return
	}

	// Clean up safety folder
	if _, err := os.Stat(tempPath); err == nil {
		os.RemoveAll(tempPath)
	}

	// Restart service if it was running
	if serviceRunning {
		if err := s.runtime.StartContainer(r.Context(), service.RuntimeID); err != nil {
			s.error(w, http.StatusInternalServerError, "restore succeeded but failed to restart service: "+err.Error())
			return
		}
	}

	s.store.CreateEvent(&api.Event{
		ID:       uuid.New().String(),
		VolumeID: &vol.ID,
		BackupID: &backup.ID,
		Type:     events.BackupRestored.String(),
		Message:  fmt.Sprintf("Successfully restored volume %s from backup %s", vol.Name, backup.ID),
	})

	s.json(w, http.StatusOK, map[string]string{"status": "restored"})
}

func (s *Server) restoreRollback(hostPath, tempPath string) {
	if _, err := os.Stat(tempPath); err == nil {
		os.RemoveAll(hostPath)
		os.Rename(tempPath, hostPath)
	}
}

func (s *Server) handleServiceLogs(w http.ResponseWriter, r *http.Request) {
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

	if svc.RuntimeID == "" {
		s.error(w, http.StatusBadRequest, "service is not deployed or has no container ID")
		return
	}

	followStr := r.URL.Query().Get("follow")
	follow := followStr == "true" || followStr == "1"

	tailStr := r.URL.Query().Get("tail")
	tail := 0
	if tailStr != "" {
		if t, err := strconv.Atoi(tailStr); err == nil {
			tail = t
		}
	}

	stream, err := s.runtime.StreamLogs(r.Context(), svc.RuntimeID, follow, tail)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "failed to get log stream: "+err.Error())
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	buffer := make([]byte, 4096)
	for {
		n, err := stream.Read(buffer)
		if n > 0 {
			w.Write(buffer[:n])
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
}
