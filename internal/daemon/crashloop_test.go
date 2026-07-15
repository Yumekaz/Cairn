package daemon

import (
	"testing"
	"time"
)

func TestCrashLoopTracker_WouldTripAndRecord(t *testing.T) {
	tr := NewCrashLoopTracker()
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	window := 10 * time.Minute
	limit := 5

	if tr.WouldTrip("svc", base, limit, window) {
		t.Fatal("empty tracker should not trip")
	}

	for i := 0; i < 4; i++ {
		now := base.Add(time.Duration(i) * time.Minute)
		count, tripped := tr.Record("svc", now, limit, window)
		if tripped {
			t.Fatalf("restart %d should not trip yet (count=%d)", i+1, count)
		}
		if count != i+1 {
			t.Fatalf("expected count %d, got %d", i+1, count)
		}
	}

	// 5th restart trips
	count, tripped := tr.Record("svc", base.Add(4*time.Minute), limit, window)
	if !tripped || count != 5 {
		t.Fatalf("5th restart: want tripped count=5, got tripped=%v count=%d", tripped, count)
	}
	if !tr.WouldTrip("svc", base.Add(5*time.Minute), limit, window) {
		t.Fatal("after 5 restarts WouldTrip should be true")
	}
}

func TestCrashLoopTracker_WindowExpiry(t *testing.T) {
	tr := NewCrashLoopTracker()
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	window := 10 * time.Minute
	limit := 3

	tr.Record("svc", base, limit, window)
	tr.Record("svc", base.Add(time.Minute), limit, window)
	// Outside window relative to a far future "now"
	far := base.Add(30 * time.Minute)
	if tr.Count("svc", far, window) != 0 {
		t.Fatalf("expected pruned count 0, got %d", tr.Count("svc", far, window))
	}
	if tr.WouldTrip("svc", far, limit, window) {
		t.Fatal("old restarts outside window must not trip")
	}
	count, tripped := tr.Record("svc", far, limit, window)
	if tripped || count != 1 {
		t.Fatalf("fresh restart after expiry: count=%d tripped=%v", count, tripped)
	}
}

func TestCrashLoopTracker_Reset(t *testing.T) {
	tr := NewCrashLoopTracker()
	now := time.Now()
	tr.Record("a", now, 5, time.Minute)
	tr.Record("b", now, 5, time.Minute)
	tr.Reset("a")
	if tr.Count("a", now, time.Minute) != 0 {
		t.Fatal("reset a should clear a")
	}
	if tr.Count("b", now, time.Minute) != 1 {
		t.Fatal("reset a must not clear b")
	}
}

func TestCrashLoopTracker_DefaultLimit(t *testing.T) {
	tr := NewCrashLoopTracker()
	base := time.Now()
	// limit 0 → DefaultCrashLoopLimit (5)
	for i := 0; i < DefaultCrashLoopLimit-1; i++ {
		_, tripped := tr.Record("x", base.Add(time.Duration(i)*time.Second), 0, DefaultCrashLoopWindow)
		if tripped {
			t.Fatalf("unexpected trip at %d", i+1)
		}
	}
	_, tripped := tr.Record("x", base.Add(time.Duration(DefaultCrashLoopLimit)*time.Second), 0, DefaultCrashLoopWindow)
	if !tripped {
		t.Fatal("expected trip at default limit")
	}
}
