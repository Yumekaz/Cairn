package duraflow

import (
	"github.com/yumekaz/cairn/internal/store"
	dfengine "github.com/yumekaz/duraflow/pkg/engine"
	dfexecutor "github.com/yumekaz/duraflow/pkg/executor"
	dfstore "github.com/yumekaz/duraflow/pkg/store"
	"path/filepath"
	"strings"
	"testing"
)

func TestInputDurableBeforeSchedulingAndWriteFailureStopsScheduling(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewStore(filepath.Join(dir, "cairn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ds := dfstore.NewSQLiteStore(filepath.Join(dir, "duraflow.db"))
	if err := ds.Init(); err != nil {
		t.Fatal(err)
	}
	defer ds.Close()
	real := dfengine.NewWorkflowEngine(ds, dfexecutor.NewRegistry())
	e := NewEngine(st, nil)
	e.SetRealEngine(real)
	e.RegisterTemplate("proof", []string{"step"}, []StepExecutor{func(*StepContext) error { return nil }})
	observed := false
	real.OnEvent = func(ev *dfstore.Event) {
		if ev.EventType != dfengine.EventWorkflowRunCreated {
			return
		}
		observed = true
		w, err := st.GetWorkflow(ev.RunID)
		if err != nil || w == nil || w.InputJSON != `{"marker":"persisted"}` {
			t.Fatalf("worker-visible run lacks durable input: %+v %v", w, err)
		}
	}
	if _, err := e.StartWorkflow("proof", map[string]string{"marker": "persisted"}); err != nil {
		t.Fatal(err)
	}
	if !observed {
		t.Fatal("no creation event")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	observed = false
	if _, err := e.StartWorkflow("proof", map[string]string{"marker": "persisted"}); err == nil || !strings.Contains(err.Error(), "persist workflow input") {
		t.Fatalf("expected persistence failure, got %v", err)
	}
	if observed {
		t.Fatal("run scheduled despite input persistence failure")
	}
}
