package models

import (
	"time"
)

// WorkflowStatus represents the lifecycle state of a workflow.
type WorkflowStatus string

const (
	WorkflowStatusPending    WorkflowStatus = "pending"
	WorkflowStatusRunning    WorkflowStatus = "running"
	WorkflowStatusCompleted  WorkflowStatus = "completed"
	WorkflowStatusFailed     WorkflowStatus = "failed"
	WorkflowStatusCancelled  WorkflowStatus = "cancelled"
)

// StepStatus represents the lifecycle state of a single workflow step.
type StepStatus string

const (
	StepStatusPending   StepStatus = "pending"
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
	StepStatusSkipped   StepStatus = "skipped"
)

// Workflow is the top-level entity that tracks a user task and its decomposed steps.
type Workflow struct {
	ID          string         `json:"id" db:"id"`
	UserTask    string         `json:"user_task" db:"user_task"`
	Status      WorkflowStatus `json:"status" db:"status"`
	FinalResult string         `json:"final_result,omitempty" db:"final_result"`
	ErrorMsg    string         `json:"error_msg,omitempty" db:"error_msg"`
	Metadata    []byte         `json:"metadata,omitempty" db:"metadata"` // JSONB
	CreatedAt   time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at" db:"updated_at"`
}

// WorkflowStep represents one atomic unit of work within a workflow.
type WorkflowStep struct {
	ID           string     `json:"id" db:"id"`
	WorkflowID   string     `json:"workflow_id" db:"workflow_id"`
	StepIndex    int        `json:"step_index" db:"step_index"`
	Name         string     `json:"name" db:"name"`
	Prompt       string     `json:"prompt" db:"prompt"`
	SystemPrompt string     `json:"system_prompt,omitempty" db:"system_prompt"`
	InputData    string     `json:"input_data,omitempty" db:"input_data"`
	OutputData   string     `json:"output_data,omitempty" db:"output_data"`
	Status       StepStatus `json:"status" db:"status"`
	RetryCount   int        `json:"retry_count" db:"retry_count"`
	ErrorMsg     string     `json:"error_msg,omitempty" db:"error_msg"`
	StartedAt    *time.Time `json:"started_at,omitempty" db:"started_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

// JobMessage is the payload enqueued to Redis for worker consumption.
type JobMessage struct {
	WorkflowID string `json:"workflow_id"`
	StepID     string `json:"step_id"`
	RetryCount int    `json:"retry_count"`
}

// CreateWorkflowRequest is the incoming HTTP request body.
type CreateWorkflowRequest struct {
	Task     string            `json:"task"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// WorkflowResponse is the API response shape for a workflow.
type WorkflowResponse struct {
	Workflow *Workflow       `json:"workflow"`
	Steps    []*WorkflowStep `json:"steps,omitempty"`
}