package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/events"
)

// retireDeployRuntime preserves backend ownership on a refused Stop. Ordinary
// deployment has already committed its healthy candidate; retirement failure is
// an explicit durable warning, not a claim that the old generation was removed.
func (s *Server) retireDeployRuntime(ctx context.Context, svc *api.Service, dep *api.Deploy, id string) error {
	err := s.runtime.StopContainer(ctx, id)
	phase := "stop"
	if err == nil {
		phase = "remove"
		err = s.runtime.RemoveContainer(ctx, id)
	}
	if err == nil {
		return nil
	}
	metadata, _ := json.Marshal(map[string]string{"runtime_id": id, "phase": phase, "error": err.Error()})
	message := fmt.Sprintf("Runtime cleanup failed for service %s (%s, %s): %v; ownership retained for verification/retry", svc.Name, id, phase, err)
	log.Printf("cairnd: %s", message)
	if eventErr := s.store.CreateEvent(&api.Event{
		ID: uuid.NewString(), ServiceID: &svc.ID, DeployID: &dep.ID,
		Type: events.RuntimeCleanupFailed.String(), Message: message, MetadataJSON: string(metadata),
	}); eventErr != nil {
		return fmt.Errorf("%s; persist cleanup warning: %w", message, eventErr)
	}
	return fmt.Errorf("%s", message)
}

func (s *Server) retirePreviousCronRuntime(ctx context.Context, svc *api.Service, dep *api.Deploy, id string) error {
	// Cron cutover may replay after a successful Remove but before its metadata
	// commit. Verify complete backend inventory first. Confirmed absence needs
	// no Stop; inventory uncertainty or an actual Stop refusal still fails.
	containers, err := s.runtime.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("verify previous cron runtime retirement: %w", err)
	}
	for _, info := range containers {
		if info == nil {
			return fmt.Errorf("cannot verify nil runtime inventory entry")
		}
		if info.ID == id || info.Name == id {
			return s.retireDeployRuntime(ctx, svc, dep, id)
		}
	}
	return nil
}
