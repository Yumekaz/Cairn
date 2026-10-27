package daemon

import (
	"fmt"

	"github.com/yumekaz/cairn/internal/store"
)

// writerJobRestoreReservation is called while cronTaskAdmissionMu is held.
// Like reconciliation, conservatively reserve all writer admission for any
// pending/running restore, including recovered or malformed workflow inputs.
// A failed inventory is uncertainty, never permission to expose a new writer.
func writerJobRestoreReservation(st *store.Store) (bool, error) {
	workflows, err := st.ListRunningWorkflows()
	if err != nil {
		return false, fmt.Errorf("cannot verify restore ownership for writer admission: %w", err)
	}
	for _, workflow := range workflows {
		if workflow.Type == "restore" {
			return true, nil
		}
	}
	return false, nil
}
