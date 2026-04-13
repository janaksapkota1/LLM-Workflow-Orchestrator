package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"llm-orchestrator/internal/models"
)

// Store wraps a pgxpool and exposes workflow/step CRUD operations.
type Store struct {
	db *pgxpool.Pool
}

// New creates a Store from an existing connection pool.
func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// Connect opens a new pgxpool using the provided DSN and pings it.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}

// ── Workflow ──────────────────────────────────────────────────────────────────

// CreateWorkflow inserts a new workflow record.
func (s *Store) CreateWorkflow(ctx context.Context, w *models.Workflow) error {
	q := `
		INSERT INTO workflows (id, user_task, status, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := s.db.Exec(ctx, q, w.ID, w.UserTask, w.Status, w.Metadata, w.CreatedAt, w.UpdatedAt)
	return err
}

// GetWorkflow fetches a single workflow by ID.
func (s *Store) GetWorkflow(ctx context.Context, id string) (*models.Workflow, error) {
	q := `
		SELECT id, user_task, status, final_result, error_msg, metadata, created_at, updated_at
		FROM workflows WHERE id = $1
	`
	w := &models.Workflow{}
	err := s.db.QueryRow(ctx, q, id).Scan(
		&w.ID, &w.UserTask, &w.Status, &w.FinalResult, &w.ErrorMsg, &w.Metadata, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get workflow %s: %w", id, err)
	}
	return w, nil
}

// UpdateWorkflowStatus updates a workflow's status and optional final result / error.
func (s *Store) UpdateWorkflowStatus(ctx context.Context, id string, status models.WorkflowStatus, finalResult, errMsg string) error {
	q := `
		UPDATE workflows
		SET status = $2, final_result = $3, error_msg = $4, updated_at = $5
		WHERE id = $1
	`
	_, err := s.db.Exec(ctx, q, id, status, finalResult, errMsg, time.Now())
	return err
}

// ListWorkflows returns recent workflows ordered by created_at desc.
func (s *Store) ListWorkflows(ctx context.Context, limit, offset int) ([]*models.Workflow, error) {
	q := `
		SELECT id, user_task, status, final_result, error_msg, created_at, updated_at
		FROM workflows ORDER BY created_at DESC LIMIT $1 OFFSET $2
	`
	rows, err := s.db.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*models.Workflow
	for rows.Next() {
		w := &models.Workflow{}
		if err := rows.Scan(&w.ID, &w.UserTask, &w.Status, &w.FinalResult, &w.ErrorMsg, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, w)
	}
	return list, rows.Err()
}

// ── Steps ─────────────────────────────────────────────────────────────────────

// CreateStep inserts a workflow step.
func (s *Store) CreateStep(ctx context.Context, step *models.WorkflowStep) error {
	q := `
		INSERT INTO workflow_steps
			(id, workflow_id, step_index, name, prompt, system_prompt, input_data, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := s.db.Exec(ctx, q,
		step.ID, step.WorkflowID, step.StepIndex, step.Name,
		step.Prompt, step.SystemPrompt, step.InputData,
		step.Status, step.CreatedAt, step.UpdatedAt,
	)
	return err
}

// GetStep fetches a step by ID.
func (s *Store) GetStep(ctx context.Context, id string) (*models.WorkflowStep, error) {
	q := `
		SELECT id, workflow_id, step_index, name, prompt, system_prompt, input_data,
		       output_data, status, retry_count, error_msg, started_at, completed_at, created_at, updated_at
		FROM workflow_steps WHERE id = $1
	`
	step := &models.WorkflowStep{}
	err := s.db.QueryRow(ctx, q, id).Scan(
		&step.ID, &step.WorkflowID, &step.StepIndex, &step.Name,
		&step.Prompt, &step.SystemPrompt, &step.InputData,
		&step.OutputData, &step.Status, &step.RetryCount, &step.ErrorMsg,
		&step.StartedAt, &step.CompletedAt, &step.CreatedAt, &step.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get step %s: %w", id, err)
	}
	return step, nil
}

// GetStepsByWorkflow returns all steps for a workflow ordered by step_index.
func (s *Store) GetStepsByWorkflow(ctx context.Context, workflowID string) ([]*models.WorkflowStep, error) {
	q := `
		SELECT id, workflow_id, step_index, name, prompt, system_prompt, input_data,
		       output_data, status, retry_count, error_msg, started_at, completed_at, created_at, updated_at
		FROM workflow_steps WHERE workflow_id = $1 ORDER BY step_index ASC
	`
	rows, err := s.db.Query(ctx, q, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []*models.WorkflowStep
	for rows.Next() {
		step := &models.WorkflowStep{}
		if err := rows.Scan(
			&step.ID, &step.WorkflowID, &step.StepIndex, &step.Name,
			&step.Prompt, &step.SystemPrompt, &step.InputData,
			&step.OutputData, &step.Status, &step.RetryCount, &step.ErrorMsg,
			&step.StartedAt, &step.CompletedAt, &step.CreatedAt, &step.UpdatedAt,
		); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

// UpdateStepStatus transitions a step to a new status, recording output/error as appropriate.
func (s *Store) UpdateStepStatus(ctx context.Context, stepID string, status models.StepStatus, output, errMsg string, retryCount int) error {
	now := time.Now()
	var startedAt, completedAt *time.Time

	switch status {
	case models.StepStatusRunning:
		startedAt = &now
	case models.StepStatusCompleted, models.StepStatusFailed, models.StepStatusSkipped:
		completedAt = &now
	}

	q := `
		UPDATE workflow_steps
		SET status       = $2,
		    output_data  = $3,
		    error_msg    = $4,
		    retry_count  = $5,
		    started_at   = COALESCE(started_at, $6),
		    completed_at = $7,
		    updated_at   = $8
		WHERE id = $1
	`
	_, err := s.db.Exec(ctx, q, stepID, status, output, errMsg, retryCount, startedAt, completedAt, now)
	return err
}
