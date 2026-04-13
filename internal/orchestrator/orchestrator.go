package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"llm-orchestrator/internal/llm"
	"llm-orchestrator/internal/models"
	"llm-orchestrator/internal/queue"
	"llm-orchestrator/internal/store"
)

const decompositionSystemPrompt = `You are a workflow planning assistant. 
Given a user task, decompose it into 3-7 sequential, actionable steps that can each be executed by an LLM.
Each step should be self-contained and its output should feed into the next step.

Respond ONLY with valid JSON in this exact format:
{
  "steps": [
    {
      "name": "short_step_name",
      "prompt": "detailed prompt for this step",
      "system_prompt": "optional system context for this step"
    }
  ]
}
Do not include any explanation or markdown outside the JSON.`

// decomposedStep is the intermediate struct parsed from the LLM decomposition response.
type decomposedStep struct {
	Name         string `json:"name"`
	Prompt       string `json:"prompt"`
	SystemPrompt string `json:"system_prompt"`
}

type decompositionResponse struct {
	Steps []decomposedStep `json:"steps"`
}

// Orchestrator coordinates workflow creation, step decomposition, and job dispatch.
type Orchestrator struct {
	store  *store.Store
	queue  *queue.Queue
	llm    *llm.Client
	logger zerolog.Logger
}

// New creates an Orchestrator.
func New(s *store.Store, q *queue.Queue, l *llm.Client, log zerolog.Logger) *Orchestrator {
	return &Orchestrator{
		store:  s,
		queue:  q,
		llm:    l,
		logger: log,
	}
}

// CreateWorkflow accepts a user task, persists a new workflow, decomposes the task
// into steps via LLM, persists those steps, and enqueues the first step for execution.
func (o *Orchestrator) CreateWorkflow(ctx context.Context, task string, metadata map[string]string) (*models.Workflow, error) {
	now := time.Now()

	var metaBytes []byte
	if len(metadata) > 0 {
		var err error
		metaBytes, err = json.Marshal(metadata)
		if err != nil {
			return nil, fmt.Errorf("marshal metadata: %w", err)
		}
	}

	wf := &models.Workflow{
		ID:        uuid.New().String(),
		UserTask:  task,
		Status:    models.WorkflowStatusPending,
		Metadata:  metaBytes,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := o.store.CreateWorkflow(ctx, wf); err != nil {
		return nil, fmt.Errorf("create workflow: %w", err)
	}

	o.logger.Info().Str("workflow_id", wf.ID).Msg("workflow created, decomposing task")

	// Decompose task into steps asynchronously so the HTTP handler returns quickly.
	go func() {
		bgCtx := context.Background()
		if err := o.decomposeAndEnqueue(bgCtx, wf, task); err != nil {
			o.logger.Error().Err(err).Str("workflow_id", wf.ID).Msg("decomposition failed")
			_ = o.store.UpdateWorkflowStatus(bgCtx, wf.ID, models.WorkflowStatusFailed, "", err.Error())
		}
	}()

	return wf, nil
}

// decomposeAndEnqueue calls the LLM to break the task into steps, saves them, and
// transitions the workflow to running by enqueuing step 0.
func (o *Orchestrator) decomposeAndEnqueue(ctx context.Context, wf *models.Workflow, task string) error {
	prompt := fmt.Sprintf("Decompose this task into executable steps:\n\n%s", task)
	result, err := o.llm.Complete(ctx, decompositionSystemPrompt, prompt)
	if err != nil {
		return fmt.Errorf("llm decompose: %w", err)
	}

	// Strip potential markdown fences before unmarshalling.
	raw := strings.TrimSpace(result.Text)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var decomposed decompositionResponse
	if err := json.Unmarshal([]byte(raw), &decomposed); err != nil {
		return fmt.Errorf("parse decomposition response: %w\nraw: %s", err, raw)
	}

	if len(decomposed.Steps) == 0 {
		return fmt.Errorf("LLM returned zero steps for workflow %s", wf.ID)
	}

	now := time.Now()
	for i, ds := range decomposed.Steps {
		step := &models.WorkflowStep{
			ID:           uuid.New().String(),
			WorkflowID:   wf.ID,
			StepIndex:    i,
			Name:         ds.Name,
			Prompt:       ds.Prompt,
			SystemPrompt: ds.SystemPrompt,
			Status:       models.StepStatusPending,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := o.store.CreateStep(ctx, step); err != nil {
			return fmt.Errorf("create step %d: %w", i, err)
		}
		o.logger.Debug().Str("step_id", step.ID).Int("index", i).Str("name", ds.Name).Msg("step created")
	}

	// Transition workflow to running and enqueue the first step.
	if err := o.store.UpdateWorkflowStatus(ctx, wf.ID, models.WorkflowStatusRunning, "", ""); err != nil {
		return fmt.Errorf("update workflow to running: %w", err)
	}

	steps, err := o.store.GetStepsByWorkflow(ctx, wf.ID)
	if err != nil || len(steps) == 0 {
		return fmt.Errorf("fetch steps after creation: %w", err)
	}

	job := &models.JobMessage{WorkflowID: wf.ID, StepID: steps[0].ID}
	return o.queue.Enqueue(ctx, job)
}

// AdvanceWorkflow is called by the worker after a step completes.
// It enqueues the next pending step or marks the workflow complete.
func (o *Orchestrator) AdvanceWorkflow(ctx context.Context, workflowID string, completedStepIndex int, stepOutput string) error {
	steps, err := o.store.GetStepsByWorkflow(ctx, workflowID)
	if err != nil {
		return fmt.Errorf("get steps: %w", err)
	}

	nextIndex := completedStepIndex + 1
	if nextIndex >= len(steps) {
		// All steps done — finalize workflow.
		return o.store.UpdateWorkflowStatus(ctx, workflowID, models.WorkflowStatusCompleted, stepOutput, "")
	}

	// Feed this step's output as input to the next.
	nextStep := steps[nextIndex]
	if nextStep.InputData == "" {
		// Hydrate input_data with previous output if not already set.
		_ = o.store.UpdateStepStatus(ctx, nextStep.ID, models.StepStatusPending, stepOutput, "", 0)
	}

	job := &models.JobMessage{WorkflowID: workflowID, StepID: nextStep.ID}
	o.logger.Info().Str("workflow_id", workflowID).Int("next_step", nextIndex).Msg("enqueuing next step")
	return o.queue.Enqueue(ctx, job)
}