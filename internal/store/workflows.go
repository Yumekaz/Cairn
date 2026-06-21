package store

import (
	"database/sql"
	"time"

	"github.com/yumekaz/cairn/internal/api"
)

// CreateWorkflow inserts a new workflow record.
func (s *Store) CreateWorkflow(w *api.Workflow) error {
	w.CreatedAt = time.Now()
	w.UpdatedAt = time.Now()

	query := `
		INSERT INTO duraflow_workflows (id, type, status, current_step_index, input_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query, w.ID, w.Type, w.Status, w.CurrentStepIndex, w.InputJSON, w.CreatedAt, w.UpdatedAt)
	return err
}

// UpdateWorkflow updates an existing workflow.
func (s *Store) UpdateWorkflow(w *api.Workflow) error {
	w.UpdatedAt = time.Now()

	query := `
		UPDATE duraflow_workflows
		SET status = ?, current_step_index = ?, updated_at = ?
		WHERE id = ?`
	_, err := s.db.Exec(query, w.Status, w.CurrentStepIndex, w.UpdatedAt, w.ID)
	return err
}

// GetWorkflow retrieves a workflow by ID, including its steps.
func (s *Store) GetWorkflow(id string) (*api.Workflow, error) {
	query := `
		SELECT id, type, status, current_step_index, input_json, created_at, updated_at
		FROM duraflow_workflows
		WHERE id = ?`
	row := s.db.QueryRow(query, id)

	var w api.Workflow
	err := row.Scan(&w.ID, &w.Type, &w.Status, &w.CurrentStepIndex, &w.InputJSON, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	steps, err := s.GetWorkflowSteps(w.ID)
	if err != nil {
		return nil, err
	}
	w.Steps = steps
	return &w, nil
}

// ListWorkflows retrieves all workflows ordered by creation date descending.
func (s *Store) ListWorkflows() ([]*api.Workflow, error) {
	query := `
		SELECT id, type, status, current_step_index, input_json, created_at, updated_at
		FROM duraflow_workflows
		ORDER BY created_at DESC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*api.Workflow
	for rows.Next() {
		var w api.Workflow
		err := rows.Scan(&w.ID, &w.Type, &w.Status, &w.CurrentStepIndex, &w.InputJSON, &w.CreatedAt, &w.UpdatedAt)
		if err != nil {
			return nil, err
		}
		list = append(list, &w)
	}
	return list, nil
}

// ListRunningWorkflows retrieves workflows marked as running or pending.
func (s *Store) ListRunningWorkflows() ([]*api.Workflow, error) {
	query := `
		SELECT id, type, status, current_step_index, input_json, created_at, updated_at
		FROM duraflow_workflows
		WHERE status = 'running' OR status = 'pending'
		ORDER BY created_at ASC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*api.Workflow
	for rows.Next() {
		var w api.Workflow
		err := rows.Scan(&w.ID, &w.Type, &w.Status, &w.CurrentStepIndex, &w.InputJSON, &w.CreatedAt, &w.UpdatedAt)
		if err != nil {
			return nil, err
		}
		list = append(list, &w)
	}
	return list, nil
}

// CreateWorkflowStep inserts a new workflow step record.
func (s *Store) CreateWorkflowStep(st *api.WorkflowStep) error {
	st.CreatedAt = time.Now()
	st.UpdatedAt = time.Now()

	query := `
		INSERT INTO duraflow_steps (id, workflow_id, step_index, name, status, error_message, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query, st.ID, st.WorkflowID, st.StepIndex, st.Name, st.Status, toNullString(&st.ErrorMessage), st.CreatedAt, st.UpdatedAt)
	return err
}

// UpdateWorkflowStep updates an existing workflow step.
func (s *Store) UpdateWorkflowStep(st *api.WorkflowStep) error {
	st.UpdatedAt = time.Now()

	query := `
		UPDATE duraflow_steps
		SET status = ?, error_message = ?, updated_at = ?
		WHERE id = ?`
	_, err := s.db.Exec(query, st.Status, toNullString(&st.ErrorMessage), st.UpdatedAt, st.ID)
	return err
}

// GetWorkflowSteps retrieves all steps for a specific workflow.
func (s *Store) GetWorkflowSteps(workflowID string) ([]*api.WorkflowStep, error) {
	query := `
		SELECT id, workflow_id, step_index, name, status, error_message, created_at, updated_at
		FROM duraflow_steps
		WHERE workflow_id = ?
		ORDER BY step_index ASC`
	rows, err := s.db.Query(query, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*api.WorkflowStep
	for rows.Next() {
		var st api.WorkflowStep
		var errMsg sql.NullString
		err := rows.Scan(&st.ID, &st.WorkflowID, &st.StepIndex, &st.Name, &st.Status, &errMsg, &st.CreatedAt, &st.UpdatedAt)
		if err != nil {
			return nil, err
		}
		st.ErrorMessage = scanString(errMsg)
		list = append(list, &st)
	}
	return list, nil
}
