package duraflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/yumekaz/cairn/internal/runtime"
	"github.com/yumekaz/cairn/internal/store"
	dfengine "github.com/yumekaz/duraflow/pkg/engine"
	dfexecutor "github.com/yumekaz/duraflow/pkg/executor"
	dfworkflow "github.com/yumekaz/duraflow/pkg/workflow"
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

// CairnExecutor implements github.com/yumekaz/duraflow/internal/executor.Executor
type CairnExecutor struct {
	store      *store.Store
	runtime    runtime.RuntimeBackend
	templates  map[string]WorkflowTemplate
	inputs     map[string]string            // maps duraflow runID to InputJSON
	runStates  map[string]map[string]string // maps duraflow runID to step state map
	mu         sync.RWMutex
}

func NewCairnExecutor(s *store.Store, r runtime.RuntimeBackend) *CairnExecutor {
	return &CairnExecutor{
		store:     s,
		runtime:   r,
		templates: make(map[string]WorkflowTemplate),
		inputs:    make(map[string]string),
		runStates: make(map[string]map[string]string),
	}
}

func (ce *CairnExecutor) RegisterTemplate(wType string, steps []string, executors []StepExecutor) {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	ce.templates[wType] = WorkflowTemplate{
		Steps:     steps,
		Executors: executors,
	}
}

func (ce *CairnExecutor) GetTemplate(wType string) (WorkflowTemplate, bool) {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	tmpl, ok := ce.templates[wType]
	return tmpl, ok
}

func (ce *CairnExecutor) RegisterRunInput(runID string, inputJSON string) {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	ce.inputs[runID] = inputJSON
}

func (ce *CairnExecutor) GetRunInput(runID string) string {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	return ce.inputs[runID]
}

func (ce *CairnExecutor) getRunState(runID string) map[string]string {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	state, exists := ce.runStates[runID]
	if !exists {
		state = make(map[string]string)
		ce.runStates[runID] = state
	}
	return state
}

func (ce *CairnExecutor) Execute(ctx context.Context, req dfexecutor.ExecutionRequest) (*dfexecutor.Result, error) {
	wType := req.Env["WORKFLOW_TYPE"]
	runID := req.Env["DURAFLOW_RUN_ID"]

	ce.mu.RLock()
	tmpl, ok := ce.templates[wType]
	inputJSON := ce.inputs[runID]
	ce.mu.RUnlock()

	if inputJSON == "" && ce.store != nil {
		w, err := ce.store.GetWorkflow(runID)
		if err == nil && w != nil {
			inputJSON = w.InputJSON
			ce.mu.Lock()
			ce.inputs[runID] = inputJSON
			ce.mu.Unlock()
		}
	}

	if !ok {
		return nil, fmt.Errorf("cairn executor: template '%s' not found", wType)
	}

	stepIdx := -1
	for idx, name := range tmpl.Steps {
		if name == req.Command {
			stepIdx = idx
			break
		}
	}

	if stepIdx == -1 {
		return nil, fmt.Errorf("cairn executor: step '%s' not found in template '%s'", req.Command, wType)
	}

	stepCtx := &StepContext{
		Context:    ctx,
		WorkflowID: runID,
		InputJSON:  inputJSON,
		Store:      ce.store,
		Runtime:    ce.runtime,
		State:      ce.getRunState(runID),
	}

	err := tmpl.Executors[stepIdx](stepCtx)
	if err != nil {
		return &dfexecutor.Result{
			ExitCode: 1,
			Stderr:   err.Error(),
			Error:    err,
		}, err
	}

	return &dfexecutor.Result{
		ExitCode: 0,
	}, nil
}

// Engine wraps CairnExecutor to keep type compatibility with existing server code.
type Engine struct {
	*CairnExecutor
	realEngine *dfengine.WorkflowEngine
}

func NewEngine(s *store.Store, r runtime.RuntimeBackend) *Engine {
	return &Engine{
		CairnExecutor: NewCairnExecutor(s, r),
	}
}

func (e *Engine) SetRealEngine(re *dfengine.WorkflowEngine) {
	e.realEngine = re
}

func (e *Engine) StartWorkflow(wType string, input interface{}) (string, error) {
	if e.realEngine == nil {
		return "", fmt.Errorf("duraflow real engine not set")
	}

	e.mu.RLock()
	tmpl, ok := e.templates[wType]
	e.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("workflow template '%s' not registered", wType)
	}

	// Build DuraFlow YAML dynamically representing the sequential steps
	yamlContent := fmt.Sprintf(`name: %s
version: 1
env:
  WORKFLOW_TYPE: %s
steps:
`, wType, wType)

	for i, step := range tmpl.Steps {
		yamlContent += fmt.Sprintf("  - id: %s\n    run: %s\n    executor: cairn\n", step, step)
		if i > 0 {
			yamlContent += "    depends_on:\n"
			yamlContent += fmt.Sprintf("      - %s\n", tmpl.Steps[i-1])
		}
	}

	def, hash, orderedSteps, err := dfworkflow.ParseAndValidate([]byte(yamlContent))
	if err != nil {
		return "", fmt.Errorf("failed to generate workflow YAML: %w", err)
	}

	inputBytes, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("failed to marshal input: %w", err)
	}

	runID := uuid.New().String()
	e.RegisterRunInput(runID, string(inputBytes))

	_, err = e.realEngine.RunWorkflowWithID(context.Background(), runID, def, orderedSteps, hash, yamlContent)
	if err != nil {
		return "", err
	}

	return runID, nil
}

func (e *Engine) ReconcileActiveWorkflows() {
	// Reconcilation is handled automatically by the DuraFlow background worker loop polling for active/pending leases
}
