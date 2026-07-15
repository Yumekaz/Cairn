package daemon

import "time"

const (
	// DefaultCrashLoopLimit is the max auto-restarts allowed in DefaultCrashLoopWindow
	// before reconcile stops thrashing and marks the service stopped.
	DefaultCrashLoopLimit = 5
	// DefaultCrashLoopWindow is the rolling window for crash-loop detection.
	DefaultCrashLoopWindow = 10 * time.Minute
)

// CrashLoopTracker records process-local auto-restart timestamps per service ID.
// Not concurrency-safe; callers must serialize access.
type CrashLoopTracker struct {
	history map[string][]time.Time
}

// NewCrashLoopTracker returns an empty tracker.
func NewCrashLoopTracker() *CrashLoopTracker {
	return &CrashLoopTracker{history: make(map[string][]time.Time)}
}

func (t *CrashLoopTracker) ensure() {
	if t.history == nil {
		t.history = make(map[string][]time.Time)
	}
}

// Count returns how many restarts for serviceID fall within window ending at now.
func (t *CrashLoopTracker) Count(serviceID string, now time.Time, window time.Duration) int {
	t.ensure()
	if window <= 0 {
		window = DefaultCrashLoopWindow
	}
	cutoff := now.Add(-window)
	n := 0
	for _, ts := range t.history[serviceID] {
		if ts.After(cutoff) || ts.Equal(cutoff) {
			n++
		}
	}
	return n
}

// WouldTrip reports whether another auto-restart would meet or exceed limit
// given restarts already recorded in the window.
func (t *CrashLoopTracker) WouldTrip(serviceID string, now time.Time, limit int, window time.Duration) bool {
	if limit <= 0 {
		limit = DefaultCrashLoopLimit
	}
	return t.Count(serviceID, now, window) >= limit
}

// Record appends a restart at now, prunes old entries, and returns the new
// in-window count and whether the limit is now met or exceeded.
func (t *CrashLoopTracker) Record(serviceID string, now time.Time, limit int, window time.Duration) (count int, tripped bool) {
	t.ensure()
	if limit <= 0 {
		limit = DefaultCrashLoopLimit
	}
	if window <= 0 {
		window = DefaultCrashLoopWindow
	}
	cutoff := now.Add(-window)
	var kept []time.Time
	for _, ts := range t.history[serviceID] {
		if ts.After(cutoff) || ts.Equal(cutoff) {
			kept = append(kept, ts)
		}
	}
	kept = append(kept, now)
	t.history[serviceID] = kept
	count = len(kept)
	tripped = count >= limit
	return count, tripped
}

// Reset clears restart history for a service (e.g. after manual start/restart).
func (t *CrashLoopTracker) Reset(serviceID string) {
	t.ensure()
	delete(t.history, serviceID)
}
