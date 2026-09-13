package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yumekaz/cairn/internal/api"
)

func (s *Server) handleRecoveryStatus(w http.ResponseWriter, r *http.Request) {
	svc, err := s.store.GetServiceByName(chi.URLParam(r, "name"))
	if err != nil {
		s.error(w, 500, err.Error())
		return
	}
	if svc == nil {
		s.error(w, 404, "service not found")
		return
	}
	deploys, err := s.store.ListDeploys(svc.ID)
	if err != nil {
		s.error(w, 500, err.Error())
		return
	}
	result := api.RecoveryStatus{Service: svc.Name, CurrentDeployID: svc.CurrentDeployID, Deployments: []api.RecoveryDeployment{}, NextActions: []string{
		"Inspect marked deployments and migration task logs; unknown task state is not proof the task stopped.",
		"Stop any surviving migration task before restoring data or submitting another migration.",
		"Verify backup integrity and schema compatibility before restore or forced rollback; backups listed are candidates, not verified recovery points.",
	}}
	for _, d := range deploys {
		if !d.StateTouched || d.Status == "success" {
			continue
		}
		suffix := d.ID
		if len(suffix) > 8 {
			suffix = suffix[:8]
		}
		item := api.RecoveryDeployment{DeployID: d.ID, Status: d.Status, Reason: d.FailureReason, TaskName: fmt.Sprintf("cairn-%s-task-%s", svc.Name, suffix), TaskState: "unknown", Volumes: []api.RecoveryVolume{}}
		if s.runtime != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			if info, err := s.runtime.InspectContainer(ctx, item.TaskName); err == nil && info != nil {
				item.TaskState = string(info.State)
			}
			cancel()
		}
		data, err := os.ReadFile(filepath.Join(s.config.DataDir, "services", svc.Name, "deploy_"+d.ID+".json"))
		var cfg api.ServiceConfig
		if err == nil {
			err = json.Unmarshal(data, &cfg)
		}
		if err != nil {
			item.ConfigError = "deployment configuration unavailable"
		} else {
			for _, v := range cfg.Volumes {
				vol, err := s.store.GetVolumeByName(v.Name)
				if err != nil {
					s.error(w, 500, err.Error())
					return
				}
				rv := api.RecoveryVolume{Name: v.Name, Backups: []*api.Backup{}}
				if vol != nil {
					backups, err := s.store.ListBackups(vol.ID)
					if err != nil {
						s.error(w, 500, err.Error())
						return
					}
					for _, b := range backups {
						if b.Status == "success" && !b.CreatedAt.After(d.CreatedAt) {
							rv.Backups = append(rv.Backups, b)
						}
					}
				}
				item.Volumes = append(item.Volumes, rv)
			}
		}
		result.Deployments = append(result.Deployments, item)
	}
	s.json(w, 200, result)
}
