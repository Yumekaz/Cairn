package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/deploymeta"
	"github.com/yumekaz/cairn/internal/duraflow"
	"github.com/yumekaz/cairn/internal/events"
	"github.com/yumekaz/cairn/internal/runtime"
)

// failDeployUnlessInterrupted marks a deploy failed only when the error is a
// real step failure. Daemon kill (context.Canceled) leaves the deploy pending
// so the next cairnd can resume the DuraFlow run.
func (s *Server) failDeployUnlessInterrupted(deploy *api.Deploy, svc *api.Service, reason string, err error) {
	if isWorkflowInterrupted(err) {
		return
	}
	s.failDeploy(deploy, svc, reason)
}

// DeployInput represents the payload for a deployment workflow.
type DeployInput struct {
	ServiceConfig     api.ServiceConfig `json:"service_config"`
	Deploy            api.Deploy        `json:"deploy"`
	Service           api.Service       `json:"service"`
	PreviousRuntimeID string            `json:"previous_runtime_id"`
}

// BackupInput represents the payload for a backup workflow.
type BackupInput struct {
	VolumeName string `json:"volume_name"`
	BackupID   string `json:"backup_id"` // Predetermined backup ID
}

// RestoreInput represents the payload for a restore workflow.
type RestoreInput struct {
	VolumeName string `json:"volume_name"`
	BackupID   string `json:"backup_id"`
}

func decodeDeployInput(inputJSON string) (*DeployInput, error) {
	var input DeployInput
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return nil, fmt.Errorf("failed to decode deploy input: %w", err)
	}
	return &input, nil
}

func decodeBackupInput(inputJSON string) (*BackupInput, error) {
	var input BackupInput
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return nil, fmt.Errorf("failed to decode backup input: %w", err)
	}
	return &input, nil
}

func decodeRestoreInput(inputJSON string) (*RestoreInput, error) {
	var input RestoreInput
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return nil, fmt.Errorf("failed to decode restore input: %w", err)
	}
	return &input, nil
}

func (s *Server) getDeployConfig(ctx *duraflow.StepContext) (*api.ServiceConfig, error) {
	if val, ok := ctx.State["config_json"]; ok {
		var cfg api.ServiceConfig
		if err := json.Unmarshal([]byte(val), &cfg); err == nil {
			return &cfg, nil
		}
	}
	input, err := decodeDeployInput(ctx.InputJSON)
	if err != nil {
		return nil, err
	}
	return &input.ServiceConfig, nil
}

// RegisterDuraFlowTemplates registers all Cairn workflows with the engine.
func (s *Server) RegisterDuraFlowTemplates(engine *duraflow.Engine) {
	// 1. Deploy Workflow
	engine.RegisterTemplate("deploy", []string{
		"validate_config",
		"save_config_on_disk",
		"pre_deploy_backup",
		"run_migration",
		"create_container",
		"start_container",
		"run_health_check",
		"route_traffic_and_cleanup",
	}, []duraflow.StepExecutor{
		s.execDeployValidateConfig,
		s.execDeploySaveConfigOnDisk,
		s.execDeployPreDeployBackup,
		s.execDeployRunMigration,
		s.execDeployCreateContainer,
		s.execDeployStartContainer,
		s.execDeployRunHealthCheck,
		s.execDeployRouteTrafficAndCleanup,
	})

	// 2. Backup Workflow
	engine.RegisterTemplate("backup", []string{
		"run_backup",
	}, []duraflow.StepExecutor{
		s.execBackupRun,
	})

	// 3. Restore Workflow
	engine.RegisterTemplate("restore", []string{
		"stop_container",
		"verify_and_extract",
		"start_container",
	}, []duraflow.StepExecutor{
		s.execRestoreStopContainer,
		s.execRestoreVerifyAndExtract,
		s.execRestoreStartContainer,
	})
}

// --- DEPLOY WORKFLOW STEP EXECUTORS ---

func (s *Server) execDeployValidateConfig(ctx *duraflow.StepContext) error {
	input, err := decodeDeployInput(ctx.InputJSON)
	if err != nil {
		return err
	}
	cfg := &input.ServiceConfig
	deploy := &input.Deploy
	svc := &input.Service

	if cfg.Kind == "cron" {
		if cfg.Schedule == "" {
			s.failDeploy(deploy, svc, "Validation failed: schedule is required for kind 'cron'")
			return fmt.Errorf("schedule is required for kind 'cron'")
		}
		if cfg.Run == "" {
			s.failDeploy(deploy, svc, "Validation failed: run command is required for kind 'cron'")
			return fmt.Errorf("run command is required for kind 'cron'")
		}
		if _, err := ParseCron(cfg.Schedule); err != nil {
			s.failDeploy(deploy, svc, "Validation failed: invalid cron schedule: "+err.Error())
			return fmt.Errorf("invalid cron schedule: %w", err)
		}
	} else if cfg.Kind == "worker" || cfg.Kind == "postgres" || cfg.Kind == "redis" || cfg.Kind == "db" {
		cfg.Ports = nil
		_ = s.store.DeleteCronJobByName(svc.Name)
	} else {
		_ = s.store.DeleteCronJobByName(svc.Name)
	}

	// Save modified config back to state
	cfgBytes, _ := json.Marshal(cfg)
	ctx.State["config_json"] = string(cfgBytes)
	return nil
}

func (s *Server) execDeploySaveConfigOnDisk(ctx *duraflow.StepContext) error {
	input, err := decodeDeployInput(ctx.InputJSON)
	if err != nil {
		return err
	}
	cfg, err := s.getDeployConfig(ctx)
	if err != nil {
		return err
	}
	deploy := &input.Deploy
	svc := &input.Service

	svcDir := filepath.Join(s.config.DataDir, "services", cfg.Name)
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		s.failDeploy(deploy, svc, "Failed to create service config directory: "+err.Error())
		return err
	}
	cfgPath := filepath.Join(svcDir, fmt.Sprintf("deploy_%s.json", deploy.ID))
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		s.failDeploy(deploy, svc, "Failed to serialize service config: "+err.Error())
		return err
	}
	if err := os.WriteFile(cfgPath, cfgJSON, 0644); err != nil {
		s.failDeploy(deploy, svc, "Failed to write service config: "+err.Error())
		return err
	}
	return nil
}

func (s *Server) execDeployPreDeployBackup(ctx *duraflow.StepContext) error {
	input, err := decodeDeployInput(ctx.InputJSON)
	if err != nil {
		return err
	}
	cfg, err := s.getDeployConfig(ctx)
	if err != nil {
		return err
	}
	deploy := &input.Deploy
	svc := &input.Service

	if cfg.Migration != "" {
		for _, volConfig := range cfg.Volumes {
			vol, err := s.store.GetVolumeByName(volConfig.Name)
			if err != nil {
				s.failDeploy(deploy, svc, "Failed to retrieve volume for backup: "+err.Error())
				return err
			}
			if vol != nil {
				backup, err := s.performVolumeBackup(vol)
				if err != nil {
					s.failDeploy(deploy, svc, fmt.Sprintf("Pre-deploy backup failed for volume %s: %s", vol.Name, err.Error()))
					return err
				}
				if backup.Status == "failed" {
					reason := backup.FailureReason
					if reason == "" {
						reason = "unknown error"
					}
					s.failDeploy(deploy, svc, fmt.Sprintf("Pre-deploy backup failed for volume %s: %s", vol.Name, reason))
					return fmt.Errorf("pre-deploy backup failed: %s", reason)
				}
			}
		}
	}
	return nil
}

func (s *Server) execDeployRunMigration(ctx *duraflow.StepContext) error {
	input, err := decodeDeployInput(ctx.InputJSON)
	if err != nil {
		return err
	}
	cfg, err := s.getDeployConfig(ctx)
	if err != nil {
		return err
	}
	deploy := &input.Deploy
	svc := &input.Service

	if cfg.Migration != "" {
		taskName := fmt.Sprintf("cairn-%s-task-%s", svc.Name, deploy.ID[:8])
		taskCfg := &api.ServiceConfig{
			Name:        cfg.Name,
			Kind:        cfg.Kind,
			Image:       cfg.Image,
			Command:     []string{"/bin/sh", "-c", cfg.Migration},
			Environment: s.resolveEnvPlaceholders(ctx.Context, s.mergeDatabaseEnvs(svc.ID, cfg.Environment)),
			Volumes:     cfg.Volumes,
		}

		taskID, err := s.runtime.CreateContainer(ctx.Context, taskCfg, taskName)
		if err != nil {
			s.failDeployUnlessInterrupted(deploy, svc, "Failed to create migration task container: "+err.Error(), err)
			return err
		}

		if err := s.runtime.StartContainer(ctx.Context, taskID); err != nil {
			if isWorkflowInterrupted(err) {
				return err
			}
			s.runtime.RemoveContainer(ctx.Context, taskID)
			s.failDeploy(deploy, svc, "Failed to start migration task container: "+err.Error())
			return err
		}

		// Wait for migration task container to exit
		var exitCode int
		var runErr error
		for {
			info, err := s.runtime.InspectContainer(ctx.Context, taskID)
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
			case <-ctx.Context.Done():
				runErr = ctx.Context.Err()
				break
			case <-time.After(200 * time.Millisecond):
			}
		}

		if runErr != nil {
			// Do not remove the migration container or fail the deploy on daemon
			// kill — leave state for resume after restart.
			if isWorkflowInterrupted(runErr) {
				return runErr
			}
			s.runtime.RemoveContainer(ctx.Context, taskID)
			s.failDeploy(deploy, svc, "Migration execution context failed: "+runErr.Error())
			return runErr
		}

		if exitCode != 0 {
			logs := s.getContainerLogs(ctx.Context, taskID)
			s.runtime.RemoveContainer(ctx.Context, taskID)
			s.failDeploy(deploy, svc, fmt.Sprintf("Migration failed with exit code %d. Logs:\n%s", exitCode, logs))
			return fmt.Errorf("migration failed (exit code %d): %s", exitCode, logs)
		}

		s.runtime.RemoveContainer(ctx.Context, taskID)
		time.Sleep(1 * time.Second)

		deploy.StateTouched = true
		if err := s.store.UpdateDeploy(deploy); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) execDeployCreateContainer(ctx *duraflow.StepContext) error {
	input, err := decodeDeployInput(ctx.InputJSON)
	if err != nil {
		return err
	}
	cfg, err := s.getDeployConfig(ctx)
	if err != nil {
		return err
	}
	deploy := &input.Deploy
	svc := &input.Service

	if cfg.Kind == "cron" {
		return nil
	}

	candidateName := fmt.Sprintf("cairn-%s-%s", svc.Name, deploy.ID[:8])
	cfg.Environment = s.resolveEnvPlaceholders(ctx.Context, s.mergeDatabaseEnvs(svc.ID, cfg.Environment))

	candidateID, err := s.runtime.CreateContainer(ctx.Context, cfg, candidateName)
	if err != nil {
		s.failDeployUnlessInterrupted(deploy, svc, "Failed to create container: "+err.Error(), err)
		return err
	}

	ctx.State["candidate_id"] = candidateID
	return nil
}

func (s *Server) execDeployStartContainer(ctx *duraflow.StepContext) error {
	input, err := decodeDeployInput(ctx.InputJSON)
	if err != nil {
		return err
	}
	cfg, err := s.getDeployConfig(ctx)
	if err != nil {
		return err
	}
	deploy := &input.Deploy
	svc := &input.Service

	if cfg.Kind == "cron" {
		return nil
	}

	candidateID := ctx.State["candidate_id"]
	if candidateID == "" {
		candidateID = fmt.Sprintf("cairn-%s-%s", svc.Name, deploy.ID[:8])
	}

	if err := s.runtime.StartContainer(ctx.Context, candidateID); err != nil {
		if isWorkflowInterrupted(err) {
			return err
		}
		s.runtime.RemoveContainer(ctx.Context, candidateID)
		s.failDeploy(deploy, svc, "Failed to start container: "+err.Error())
		return err
	}
	return nil
}

func (s *Server) execDeployRunHealthCheck(ctx *duraflow.StepContext) error {
	input, err := decodeDeployInput(ctx.InputJSON)
	if err != nil {
		return err
	}
	cfg, err := s.getDeployConfig(ctx)
	if err != nil {
		return err
	}
	deploy := &input.Deploy
	svc := &input.Service

	if cfg.Kind == "cron" {
		return nil
	}

	candidateID := ctx.State["candidate_id"]
	if candidateID == "" {
		candidateID = fmt.Sprintf("cairn-%s-%s", svc.Name, deploy.ID[:8])
	}

	hostPort := 0
	if len(cfg.Ports) > 0 {
		hostPort = cfg.Ports[0].Host
	}

	if hostPort > 0 && cfg.HealthCheck != nil {
		info, err := s.runtime.InspectContainer(ctx.Context, candidateID)
		if err != nil {
			s.failDeployUnlessInterrupted(deploy, svc, "Failed to inspect container: "+err.Error(), err)
			return err
		}
		if err := RunHealthCheck(ctx.Context, cfg.HealthCheck, info.IPAddress, cfg.Ports[0].Container); err != nil {
			if isWorkflowInterrupted(err) {
				return err
			}
			logs := s.getContainerLogs(ctx.Context, candidateID)
			s.runtime.StopContainer(ctx.Context, candidateID)
			s.runtime.RemoveContainer(ctx.Context, candidateID)
			s.failDeploy(deploy, svc, fmt.Sprintf("Health check failed: %s. Logs:\n%s", err.Error(), logs))
			return fmt.Errorf("container health checks failed: %w", err)
		}
	}
	return nil
}

func (s *Server) execDeployRouteTrafficAndCleanup(ctx *duraflow.StepContext) error {
	input, err := decodeDeployInput(ctx.InputJSON)
	if err != nil {
		return err
	}
	cfg, err := s.getDeployConfig(ctx)
	if err != nil {
		return err
	}
	deploy := &input.Deploy
	svc := &input.Service
	previousRuntimeID := input.PreviousRuntimeID

	if dbDeploy, err := s.store.GetDeploy(deploy.ID); err == nil && dbDeploy != nil {
		deploy.StateTouched = dbDeploy.StateTouched
	}

	if cfg.Kind == "cron" {
		if previousRuntimeID != "" {
			_ = s.runtime.StopContainer(ctx.Context, previousRuntimeID)
			_ = s.runtime.RemoveContainer(ctx.Context, previousRuntimeID)
		}

		cronJob := &api.CronJob{
			ID:        uuid.New().String(),
			ServiceID: svc.ID,
			Name:      svc.Name,
			Schedule:  cfg.Schedule,
			Command:   cfg.Run,
		}
		if err := s.store.UpsertCronJob(cronJob); err != nil {
			s.failDeploy(deploy, svc, "Failed to register cron job: "+err.Error())
			return err
		}

		deploy.Status = "success"
		deploy.Stage = "completed"
		deploy.HealthStatus = "healthy"
		timeNow := time.Now()
		deploy.CompletedAt = &timeNow
		_ = s.store.UpdateDeploy(deploy)

		svc.RuntimeID = ""
		svc.DesiredState = "active"
		svc.ActualState = "active"
		svc.CurrentDeployID = deploymeta.AfterSuccess(deploy.ID)
		svc.Route = "N/A"
		_ = s.store.UpsertService(svc)

		_ = s.store.CreateEvent(&api.Event{
			ID:        uuid.New().String(),
			ServiceID: &svc.ID,
			DeployID:  &deploy.ID,
			Type:      events.DeploySucceeded.String(),
			Message:   fmt.Sprintf("Successfully registered scheduled cron job for service %s", svc.Name),
		})
		return nil
	}

	candidateID := ctx.State["candidate_id"]
	if candidateID == "" {
		candidateID = fmt.Sprintf("cairn-%s-%s", svc.Name, deploy.ID[:8])
	}

	deployID := deploy.ID
	svcID := svc.ID
	if deployID == "" {
		return fmt.Errorf("route_traffic: empty deploy id")
	}

	// Prefer real container ID from inspect when State lost candidate_id after crash.
	if info, err := s.runtime.InspectContainer(ctx.Context, candidateID); err == nil && info != nil && info.ID != "" {
		candidateID = info.ID
	}

	// Commit metadata FIRST so a hang/failure stopping the old container cannot
	// leave the deploy stuck in pending after a successful candidate is live.
	if err := s.store.MarkDeployTerminal(deployID, "success", "healthy", ""); err != nil {
		return fmt.Errorf("failed to mark deploy success: %w", err)
	}
	log.Printf("cairnd: deploy %s marked success (route_traffic)", deployID)

	hostPort := 0
	if len(cfg.Ports) > 0 {
		hostPort = cfg.Ports[0].Host
	}

	dbSvc, err := s.store.GetService(svcID)
	if err != nil || dbSvc == nil {
		return fmt.Errorf("route_traffic: load service %s: %w", svcID, err)
	}
	dbSvc.RuntimeID = candidateID
	dbSvc.DesiredState = "running"
	dbSvc.ActualState = "running"
	dbSvc.CurrentDeployID = deploymeta.AfterSuccess(deployID)
	if hostPort > 0 {
		dbSvc.Route = fmt.Sprintf("http://localhost:%d", hostPort)
	} else {
		dbSvc.Route = "N/A"
	}
	if err := s.store.UpsertService(dbSvc); err != nil {
		return fmt.Errorf("failed to update service after deploy: %w", err)
	}

	if check, err := s.store.GetDeploy(deployID); err != nil || check == nil || check.Status != "success" {
		return fmt.Errorf("deploy %s not success after write (got %v, err %v)", deployID, check, err)
	}

	// Best-effort cleanup of previous runtime (after metadata commit).
	if previousRuntimeID != "" && previousRuntimeID != candidateID {
		_ = s.runtime.StopContainer(ctx.Context, previousRuntimeID)
		_ = s.runtime.RemoveContainer(ctx.Context, previousRuntimeID)
	}

	candidateName := fmt.Sprintf("cairn-%s-%s", dbSvc.Name, deployID[:8])
	depID := deployID
	_ = s.store.CreateEvent(&api.Event{
		ID:        uuid.New().String(),
		ServiceID: &dbSvc.ID,
		DeployID:  &depID,
		Type:      events.DeploySucceeded.String(),
		Message:   fmt.Sprintf("Successfully deployed service %s (container: %s)", dbSvc.Name, candidateName),
	})
	return nil
}

// --- BACKUP WORKFLOW STEP EXECUTORS ---

func (s *Server) execBackupRun(ctx *duraflow.StepContext) error {
	input, err := decodeBackupInput(ctx.InputJSON)
	if err != nil {
		return err
	}

	vol, err := s.store.GetVolumeByName(input.VolumeName)
	if err != nil {
		return err
	}
	if vol == nil {
		return fmt.Errorf("volume not found: %s", input.VolumeName)
	}

	if err := os.MkdirAll(vol.HostPath, 0755); err != nil {
		return fmt.Errorf("failed to create host volume directory: %w", err)
	}

	backupDir := filepath.Join(s.config.BackupDir, vol.Name)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	backupPath := filepath.Join(backupDir, input.BackupID+".tar.gz")

	b := &api.Backup{
		ID:         input.BackupID,
		VolumeID:   vol.ID,
		BackupPath: backupPath,
		Status:     "pending",
		CreatedAt:  time.Now(),
	}

	if err := s.store.CreateBackup(b); err != nil {
		return fmt.Errorf("failed to create backup record: %w", err)
	}

	s.store.CreateEvent(&api.Event{
		ID:       uuid.New().String(),
		VolumeID: &vol.ID,
		BackupID: &b.ID,
		Type:     events.BackupStarted.String(),
		Message:  fmt.Sprintf("Started backup for volume %s (ID: %s)", vol.Name, b.ID),
	})

	var isLogicalDbRunning bool
	var dbType string // "postgres", "redis", "mongodb"
	var dbService *api.Service
	var dbServiceConfig *api.ServiceConfig

	if vol.AttachedServiceID != "" {
		svc, err := s.store.GetService(vol.AttachedServiceID)
		if err == nil && svc != nil && (svc.Kind == "postgres" || svc.Kind == "redis" || svc.Kind == "mongodb") && svc.ActualState == "running" && svc.RuntimeID != "" {
			cfgPath := filepath.Join(s.config.DataDir, "services", svc.Name, fmt.Sprintf("deploy_%s.json", svc.CurrentDeployID))
			cfgJSON, err := os.ReadFile(cfgPath)
			if err == nil {
				var cfg api.ServiceConfig
				if err := json.Unmarshal(cfgJSON, &cfg); err == nil {
					isLogicalDbRunning = true
					dbType = svc.Kind
					dbService = svc
					dbServiceConfig = &cfg
				}
			}
		}
	}

	var checksum string
	var sizeBytes int64

	if isLogicalDbRunning {
		switch dbType {
		case "postgres":
			checksum, sizeBytes, err = s.performPostgresDumpBackup(vol, dbService, dbServiceConfig, backupPath, b.ID)
		case "redis":
			checksum, sizeBytes, err = s.performRedisDumpBackup(vol, dbService, dbServiceConfig, backupPath, b.ID)
		case "mongodb":
			checksum, sizeBytes, err = s.performMongoDumpBackup(vol, dbService, dbServiceConfig, backupPath, b.ID)
		}
	} else {
		checksum, sizeBytes, err = CreateTarGz(vol.HostPath, backupPath)
	}

	if err != nil {
		b.Status = "failed"
		b.FailureReason = err.Error()
		s.store.UpdateBackup(b)

		s.store.CreateEvent(&api.Event{
			ID:       uuid.New().String(),
			VolumeID: &vol.ID,
			BackupID: &b.ID,
			Type:     events.BackupFailed.String(),
			Message:  fmt.Sprintf("Backup failed for volume %s: %s", vol.Name, err.Error()),
		})
		return err
	}

	b.Status = "success"
	b.SizeBytes = sizeBytes
	b.Checksum = checksum
	timeNow := time.Now()
	b.CompletedAt = &timeNow
	s.store.UpdateBackup(b)

	s.store.CreateEvent(&api.Event{
		ID:       uuid.New().String(),
		VolumeID: &vol.ID,
		BackupID: &b.ID,
		Type:     events.BackupSucceeded.String(),
		Message:  fmt.Sprintf("Successfully completed backup for volume %s (ID: %s)", vol.Name, b.ID),
	})

	return nil
}

// --- RESTORE WORKFLOW STEP EXECUTORS ---

func (s *Server) execRestoreStopContainer(ctx *duraflow.StepContext) error {
	input, err := decodeRestoreInput(ctx.InputJSON)
	if err != nil {
		return err
	}

	vol, err := s.resolveVolumeOrServiceVolume(input.VolumeName)
	if err != nil {
		return err
	}
	if vol == nil {
		return fmt.Errorf("volume not found: %s", input.VolumeName)
	}

	backup, err := s.store.GetBackup(input.BackupID)
	if err != nil {
		return err
	}
	if backup == nil || backup.VolumeID != vol.ID {
		return fmt.Errorf("backup does not exist or does not belong to volume")
	}

	s.store.CreateEvent(&api.Event{
		ID:       uuid.New().String(),
		VolumeID: &vol.ID,
		BackupID: &backup.ID,
		Type:     events.BackupStarted.String(),
		Message:  fmt.Sprintf("Restoring volume %s from backup %s", vol.Name, backup.ID),
	})

	var serviceRunning bool
	var dbType string // "postgres", "redis", "mongodb"

	if vol.AttachedServiceID != "" {
		service, err := s.store.GetService(vol.AttachedServiceID)
		if err == nil && service != nil {
			dbType = service.Kind
			if dbType == "postgres" || dbType == "mongodb" {
				// Keep database container running for logical restore commands
			} else if service.ActualState == "running" && service.RuntimeID != "" {
				serviceRunning = true
				if err := s.runtime.StopContainer(ctx.Context, service.RuntimeID); err != nil {
					return fmt.Errorf("failed to stop service container: %w", err)
				}
			}
		}
	}

	ctx.State["db_type"] = dbType
	ctx.State["service_running"] = fmt.Sprintf("%v", serviceRunning)
	return nil
}

func (s *Server) execRestoreVerifyAndExtract(ctx *duraflow.StepContext) error {
	input, err := decodeRestoreInput(ctx.InputJSON)
	if err != nil {
		return err
	}

	vol, err := s.resolveVolumeOrServiceVolume(input.VolumeName)
	if err != nil {
		return err
	}

	backup, err := s.store.GetBackup(input.BackupID)
	if err != nil {
		return err
	}

	dbType := ctx.State["db_type"]
	isLogicalRestore := dbType == "postgres" || dbType == "mongodb"
	serviceRunning := ctx.State["service_running"] == "true"

	var service *api.Service
	if vol.AttachedServiceID != "" {
		service, _ = s.store.GetService(vol.AttachedServiceID)
	}

	if isLogicalRestore {
		if service == nil || service.RuntimeID == "" {
			return fmt.Errorf("database container has not been initialized")
		}

		dbInfo, err := s.runtime.InspectContainer(ctx.Context, service.RuntimeID)
		if err != nil {
			return fmt.Errorf("failed to inspect database: %w", err)
		}
		if dbInfo.State != runtime.StateRunning {
			if err := s.runtime.StartContainer(ctx.Context, service.RuntimeID); err != nil {
				return fmt.Errorf("failed to start database: %w", err)
			}
			service.ActualState = "running"
			_ = s.store.UpsertService(service)
		}

		// Verify checksum
		calcSum, err := ComputeFileSha256(backup.BackupPath)
		if err != nil {
			return fmt.Errorf("checksum calculation failed: %w", err)
		}
		if calcSum != backup.Checksum {
			return fmt.Errorf("checksum mismatch: expected %s, got %s", backup.Checksum, calcSum)
		}

		var restoreErr error
		if dbType == "postgres" {
			restoreErr = s.performPostgresRestore(ctx.Context, vol, service, backup)
		} else if dbType == "mongodb" {
			restoreErr = s.performMongoRestore(ctx.Context, vol, service, backup)
		}

		if restoreErr != nil {
			return fmt.Errorf("logical database restore failed: %w", restoreErr)
		}

		s.store.CreateEvent(&api.Event{
			ID:       uuid.New().String(),
			VolumeID: &vol.ID,
			BackupID: &backup.ID,
			Type:     events.BackupRestored.String(),
			Message:  fmt.Sprintf("Successfully restored volume %s from backup %s", vol.Name, backup.ID),
		})
		return nil
	}

	tempPath := vol.HostPath + ".tmp_restore_bak"
	if _, err := os.Stat(vol.HostPath); err == nil {
		if err := os.Rename(vol.HostPath, tempPath); err != nil {
			if serviceRunning && service != nil {
				_ = s.runtime.StartContainer(ctx.Context, service.RuntimeID)
			}
			return fmt.Errorf("failed to move volume directory to safety: %w", err)
		}
	}

	calcSum, err := ComputeFileSha256(backup.BackupPath)
	if err != nil {
		s.restoreRollback(vol.HostPath, tempPath)
		if serviceRunning && service != nil {
			_ = s.runtime.StartContainer(ctx.Context, service.RuntimeID)
		}
		return fmt.Errorf("failed to compute checksum: %w", err)
	}

	if calcSum != backup.Checksum {
		s.restoreRollback(vol.HostPath, tempPath)
		if serviceRunning && service != nil {
			_ = s.runtime.StartContainer(ctx.Context, service.RuntimeID)
		}
		return fmt.Errorf("checksum mismatch: expected %s, got %s", backup.Checksum, calcSum)
	}

	if dbType == "redis" {
		_ = os.MkdirAll(vol.HostPath, 0755)
		if err := DecompressGzipToFile(backup.BackupPath, filepath.Join(vol.HostPath, "dump.rdb")); err != nil {
			s.restoreRollback(vol.HostPath, tempPath)
			if serviceRunning && service != nil {
				_ = s.runtime.StartContainer(ctx.Context, service.RuntimeID)
			}
			return fmt.Errorf("failed to decompress Redis rdb: %w", err)
		}
	} else {
		if err := ExtractTarGz(backup.BackupPath, vol.HostPath); err != nil {
			s.restoreRollback(vol.HostPath, tempPath)
			if serviceRunning && service != nil {
				_ = s.runtime.StartContainer(ctx.Context, service.RuntimeID)
			}
			return fmt.Errorf("failed to extract tarball: %w", err)
		}
	}

	if _, err := os.Stat(tempPath); err == nil {
		os.RemoveAll(tempPath)
	}
	return nil
}

func (s *Server) execRestoreStartContainer(ctx *duraflow.StepContext) error {
	input, err := decodeRestoreInput(ctx.InputJSON)
	if err != nil {
		return err
	}

	vol, err := s.resolveVolumeOrServiceVolume(input.VolumeName)
	if err != nil {
		return err
	}

	backup, err := s.store.GetBackup(input.BackupID)
	if err != nil {
		return err
	}

	dbType := ctx.State["db_type"]
	isLogicalRestore := dbType == "postgres" || dbType == "mongodb"
	serviceRunning := ctx.State["service_running"] == "true"

	if isLogicalRestore {
		return nil
	}

	if serviceRunning && vol.AttachedServiceID != "" {
		service, err := s.store.GetService(vol.AttachedServiceID)
		if err == nil && service != nil && service.RuntimeID != "" {
			if err := s.runtime.StartContainer(ctx.Context, service.RuntimeID); err != nil {
				return fmt.Errorf("restore succeeded but failed to restart service: %w", err)
			}
		}
	}

	s.store.CreateEvent(&api.Event{
		ID:       uuid.New().String(),
		VolumeID: &vol.ID,
		BackupID: &backup.ID,
		Type:     events.BackupRestored.String(),
		Message:  fmt.Sprintf("Successfully restored volume %s from backup %s", vol.Name, backup.ID),
	})
	return nil
}
