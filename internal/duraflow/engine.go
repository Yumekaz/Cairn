package duraflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/google/uuid"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/runtime"
	"github.com/yumekaz/cairn/internal/store"
)

// StepContext is the payload passed to each step execution block.
type StepContext struct {
	Context    context.Context
	WorkflowID string
	InputJSON  string
	Store      *store.Store
	Runtime    runtime.RuntimeBackend
	State      map[string]string
}

// StepExecutor executes a single step of a workflow.
type StepExecutor func(ctx *StepContext) error

// WorkflowTemplate defines the sequence of steps for a workflow.
type WorkflowTemplate struct {
	Steps     []string
	Executors []StepExecutor
}

// Engine coordinates registering templates and executing workflows durably.
type Engine struct {
	store     *store.Store
	runtime   runtime.RuntimeBackend
	templates map[string]WorkflowTemplate
	active    map[string]context.CancelFunc
	mu        sync.Mutex
}

// NewEngine initializes a DuraFlow engine instance.
func NewEngine(s *store.Store, r runtime.RuntimeBackend) *Engine {
	return &Engine{
		store:     s,
		runtime:   r,
		templates: make(map[string]WorkflowTemplate),
		active:    make(map[string]context.CancelFunc),
	}
}

// RegisterTemplate registers a workflow type template.
func (e *Engine) RegisterTemplate(wType string, steps []string, executors []StepExecutor) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.templates[wType] = WorkflowTemplate{
		Steps:     steps,
		Executors: executors,
	}
}

// StartWorkflow creates and begins execution of a workflow.
func (e *Engine) StartWorkflow(wType string, input interface{}) (string, error) {
	e.mu.Lock()
	template, ok := e.templates[wType]
	e.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("workflow template '%s' not registered", wType)
	}

	inputBytes, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("failed to marshal workflow input: %w", err)
	}

	workflowID := uuid.New().String()
	w := &api.Workflow{
		ID:               workflowID,
		Type:             wType,
		Status:           "pending",
		CurrentStepIndex: 0,
		InputJSON:        string(inputBytes),
	}

	if err := e.store.CreateWorkflow(w); err != nil {
		return "", err
	}

	// Create step records
	var steps []*api.WorkflowStep
	for idx, name := range template.Steps {
		step := &api.WorkflowStep{
			ID:         uuid.New().String(),
			WorkflowID: workflowID,
			StepIndex:  idx,
			Name:       name,
			Status:     "pending",
		}
		if err := e.store.CreateWorkflowStep(step); err != nil {
			return "", err
}
		steps = append(steps, step)
	}

	// Begin background execution
	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	e.active[workflowID] = cancel
	e.mu.Unlock()

	go e.executeWorkflow(ctx, w, steps, template)

	return workflowID, nil
}

// ReconcileActiveWorkflows scans for active workflows and resumes them.
func (e *Engine) ReconcileActiveWorkflows() {
	list, err := e.store.ListRunningWorkflows()
	if err != nil {
		log.Printf("duraflow: failed to query running workflows: %v", err)
		return
	}

	if len(list) == 0 {
		return
	}

	log.Printf("duraflow: found %d in-flight workflow(s) to reconcile...", len(list))

	for _, w := range list {
		e.mu.Lock()
		template, ok := e.templates[w.Type]
		e.mu.Unlock()

		if !ok {
			log.Printf("duraflow: failed to reconcile workflow %s: template '%s' not found", w.ID, w.Type)
			w.Status = "failed"
			_ = e.store.UpdateWorkflow(w)
			continue
		}

		steps, err := e.store.GetWorkflowSteps(w.ID)
		if err != nil {
			log.Printf("duraflow: failed to read steps for workflow %s: %v", w.ID, err)
			continue
		}

		ctx, cancel := context.WithCancel(context.Background())
		e.mu.Lock()
		e.active[w.ID] = cancel
		e.mu.Unlock()

		log.Printf("duraflow: resuming workflow %s (%s) from step index %d", w.ID, w.Type, w.CurrentStepIndex)
		go e.executeWorkflow(ctx, w, steps, template)
	}
}

// executeWorkflow runs the step loop.
func (e *Engine) executeWorkflow(ctx context.Context, w *api.Workflow, steps []*api.WorkflowStep, template WorkflowTemplate) {
	defer func() {
		e.mu.Lock()
		delete(e.active, w.ID)
		e.mu.Unlock()
	}()

	state := make(map[string]string)
	stepCtx := &StepContext{
		Context:    ctx,
		WorkflowID: w.ID,
		InputJSON:  w.InputJSON,
		Store:      e.store,
		Runtime:    e.runtime,
		State:      state,
	}

	w.Status = "running"
	_ = e.store.UpdateWorkflow(w)

	for i := w.CurrentStepIndex; i < len(template.Steps); i++ {
		if err := ctx.Err(); err != nil {
			return
		}

		step := steps[i]
		step.Status = "running"
		_ = e.store.UpdateWorkflowStep(step)

		exec := template.Executors[i]
		err := exec(stepCtx)

		if err != nil {
			if ctx.Err() != nil {
				// Interrupted by daemon context cancellation (e.g., shutdown/restart)
				// Do not mark workflow/step as failed, keep status as running/pending so it can be resumed
				log.Printf("duraflow: workflow %s execution interrupted by context cancellation: %v", w.ID, err)
				return
			}
			log.Printf("duraflow: workflow %s failed at step '%s': %v", w.ID, step.Name, err)
			step.Status = "failed"
			step.ErrorMessage = err.Error()
			_ = e.store.UpdateWorkflowStep(step)

			w.Status = "failed"
			_ = e.store.UpdateWorkflow(w)
			return
		}

		step.Status = "success"
		_ = e.store.UpdateWorkflowStep(step)

		w.CurrentStepIndex = i + 1
		if w.CurrentStepIndex == len(template.Steps) {
			w.Status = "success"
		}
		_ = e.store.UpdateWorkflow(w)
	}

	log.Printf("duraflow: workflow %s completed with status '%s'", w.ID, w.Status)
}
