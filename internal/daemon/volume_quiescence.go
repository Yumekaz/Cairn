package daemon

import (
	"context"
	"fmt"
	"sort"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/duraflow"
)

// requireCompletedServiceJobs fails closed: a finished timestamp or a generic
// runtime error is not evidence that a task which can write this volume exited.
func (s *Server) requireCompletedServiceJobs(serviceID string) error {
	jobs, err := s.store.ListJobRunsByService(serviceID)
	if err != nil {
		return fmt.Errorf("verify service job completion: %w", err)
	}
	for _, job := range jobs {
		if (job.Status != "success" && job.Status != "failed") || job.FinishedAt == nil || job.ExitCode == nil || *job.ExitCode < 0 {
			return fmt.Errorf("job %s is active or its completion is unverified", job.ID)
		}
	}
	return nil
}

// ownedServiceRuntimeIDs selects exact names backed by this service's persisted
// deployments and jobs, not a name-prefix heuristic. A previous generation may
// still own a volume after best-effort retirement failed. StateStopped/Error is
// deliberately NOT treated as a successful Stop acknowledgement.
func (s *Server) ownedServiceRuntimeIDs(ctx context.Context, svc *api.Service, primaryRequired ...bool) ([]string, error) {
	deploys, err := s.store.ListDeploys(svc.ID)
	if err != nil {
		return nil, fmt.Errorf("list service deployments: %w", err)
	}
	jobs, err := s.store.ListJobRunsByService(svc.ID)
	if err != nil {
		return nil, fmt.Errorf("list service jobs: %w", err)
	}
	names := make(map[string]bool)
	for _, dep := range deploys {
		if len(dep.ID) < 8 {
			return nil, fmt.Errorf("cannot verify runtime ownership for invalid deployment id %q", dep.ID)
		}
		names[fmt.Sprintf("cairn-%s-%s", svc.Name, dep.ID[:8])] = true
	}
	for _, job := range jobs {
		if len(job.ID) < 8 {
			return nil, fmt.Errorf("cannot verify runtime ownership for invalid job id %q", job.ID)
		}
		var kind string
		switch job.Type {
		case "cron":
			kind = "cron"
		case "one-off":
			kind = "oneoff"
		default:
			return nil, fmt.Errorf("cannot verify runtime ownership for job %s of type %q", job.ID, job.Type)
		}
		names[fmt.Sprintf("cairn-%s-%s-%s", svc.Name, kind, job.ID[:8])] = true
	}
	containers, err := s.runtime.ListContainers(ctx)
	if err != nil {
		return nil, fmt.Errorf("verify owned runtime inventory: %w", err)
	}
	primary := svc.RuntimeID
	primaryPresent := false
	owned := make(map[string]bool)
	for _, info := range containers {
		if info == nil {
			return nil, fmt.Errorf("cannot verify nil runtime inventory entry")
		}
		if info.Name == svc.RuntimeID && info.ID != "" {
			primary = info.ID
			primaryPresent = true
		}
		if info.ID == svc.RuntimeID && svc.RuntimeID != "" {
			primaryPresent = true
		}
		if names[info.Name] {
			if info.ID == "" {
				return nil, fmt.Errorf("owned runtime %s has no verified id", info.Name)
			}
			owned[info.ID] = true
		}
	}
	delete(owned, primary)
	ids := make([]string, 0, len(owned)+1)
	for id := range owned {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if primary != "" && (len(primaryRequired) == 0 || primaryRequired[0] || primaryPresent) {
		ids = append([]string{primary}, ids...)
	}
	return ids, nil
}

// Caller holds lifecycleMu and cronTaskAdmissionMu. Current implementation
// supports a single recorded volume owner; unidentified/active writers refuse
// physical restore rather than assuming that cached ActualState is current.
func (s *Server) quiesceVolumeOwner(ctx context.Context, vol *api.Volume) (*api.Service, error) {
	if vol.AttachedServiceID == "" {
		return nil, nil
	}
	svc, err := s.store.GetService(vol.AttachedServiceID)
	if err != nil {
		return nil, fmt.Errorf("load volume owner: %w", err)
	}
	if svc == nil {
		return nil, fmt.Errorf("volume owner runtime identity is missing")
	}
	if svc.RuntimeID == "" {
		if svc.Kind != "cron" {
			return nil, fmt.Errorf("volume owner runtime identity is missing")
		}
		// Registered cron services intentionally have no primary container.
		// Their exact recorded job runtimes are still inventoried/fenced below.
		job, err := s.store.GetCronJobByName(svc.Name)
		if err != nil {
			return nil, fmt.Errorf("verify cron volume owner: %w", err)
		}
		if job == nil || job.ServiceID != svc.ID {
			return nil, fmt.Errorf("cron volume owner schedule is missing")
		}
	}
	activeDeploys, err := s.store.ListActiveDeployIDs()
	if err != nil {
		return nil, fmt.Errorf("verify deployment writer activity: %w", err)
	}
	if len(activeDeploys) != 0 {
		return nil, fmt.Errorf("cannot physically restore while deployment work is active")
	}
	if err := s.requireCompletedServiceJobs(svc.ID); err != nil {
		return nil, err
	}
	ids, err := s.ownedServiceRuntimeIDs(ctx, svc)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := s.runtime.StopContainer(ctx, id); err != nil {
			return nil, fmt.Errorf("failed to quiesce owned service container %s: %w", id, err)
		}
	}
	return svc, nil
}

func (s *Server) restartPhysicalRestoreOwner(ctx *duraflow.StepContext, vol *api.Volume) error {
	svc, err := s.store.GetService(vol.AttachedServiceID)
	if err != nil {
		return fmt.Errorf("load current volume owner: %w", err)
	}
	if svc == nil || svc.RuntimeID == "" {
		return fmt.Errorf("volume owner runtime identity is missing")
	}
	// A Stop may be acknowledged between durable steps. Old workflow state must
	// not resurrect a service that its operator now wants stopped.
	if svc.DesiredState != "running" {
		return nil
	}
	return s.runtime.StartContainer(ctx.Context, svc.RuntimeID)
}
