package worker

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/rs/zerolog"
	"llm-orchestrator/internal/llm"
	"llm-orchestrator/internal/models"
	"llm-orchestrator/internal/orchestrator"
	"llm-orchestrator/internal/queue"
	"llm-orchestrator/internal/store"
)

// Worker dequeues jobs and executes LLM steps with retry and backoff.
type Worker struct {
	id           int
	store        *store.Store
	queue        *queue.Queue
	llm          *llm.Client
	orch         *orchestrator.Orchestrator
	maxRetries   int
	logger       zerolog.Logger
}

// New creates a single worker instance.
func New(id int, s *store.Store, q *queue.Queue, l *llm.Client, o *orchestrator.Orchestrator, maxRetries int, log zerolog.Logger) *Worker {
	return &Worker{
		id:         id,
		store:      s,
		queue:      q,
		llm:        l,
		orch:       o,
		maxRetries: maxRetries,
		logger:     log.With().Int("worker_id", id).Logger(),
	}
}

// Run loops, dequeuing and processing jobs until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info().Msg("worker started")
	for {
		select {
		case <-ctx.Done():
			w.logger.Info().Msg("worker stopping")
			return
		default:
		}

		job, err := w.queue.Dequeue(ctx, 2*time.Second)
		if err != nil {
			w.logger.Error().Err(err).Msg("dequeue error")
			continue
		}
		if job == nil {
			// Timeout — promote any due retries then loop.
			promoted, _ := w.queue.PromoteDueRetries(ctx)
			if promoted > 0 {
				w.logger.Debug().Int("promoted", promoted).Msg("promoted retry jobs")
			}
			continue
		}

		w.logger.Info().
			Str("workflow_id", job.WorkflowID).
			Str("step_id", job.StepID).
			Int("retry", job.RetryCount).
			Msg("processing job")

		if err := w.processJob(ctx, job); err != nil {
			w.logger.Error().Err(err).Str("step_id", job.StepID).Msg("job processing error")
		}
	}
}

// processJob handles a single job: fetches step, runs LLM, updates state, advances workflow.
func (w *Worker) processJob(ctx context.Context, job *models.JobMessage) error {
	step, err := w.store.GetStep(ctx, job.StepID)
	if err != nil {
		return fmt.Errorf("get step: %w", err)
	}

	// Skip already-completed or cancelled steps (idempotency guard).
	if step.Status == models.StepStatusCompleted || step.Status == models.StepStatusSkipped {
		w.logger.Warn().Str("step_id", step.ID).Str("status", string(step.Status)).Msg("step already processed, skipping")
		return nil
	}

	// Mark running.
	if err := w.store.UpdateStepStatus(ctx, step.ID, models.StepStatusRunning, "", "", job.RetryCount); err != nil {
		return fmt.Errorf("mark step running: %w", err)
	}

	// Build prompt, optionally injecting previous step output.
	prompt := step.Prompt
	if step.InputData != "" {
		prompt = fmt.Sprintf("Previous step output:\n%s\n\n---\n\nYour task:\n%s", step.InputData, step.Prompt)
	}

	// Execute LLM call.
	result, llmErr := w.llm.Complete(ctx, step.SystemPrompt, prompt)

	if llmErr != nil {
		return w.handleFailure(ctx, job, step, llmErr)
	}

	// Mark step completed.
	if err := w.store.UpdateStepStatus(ctx, step.ID, models.StepStatusCompleted, result.Text, "", job.RetryCount); err != nil {
		return fmt.Errorf("mark step completed: %w", err)
	}

	w.logger.Info().
		Str("step_id", step.ID).
		Int("input_tokens", result.InputTokens).
		Int("output_tokens", result.OutputTokens).
		Msg("step completed")

	// Advance workflow to next step or completion.
	return w.orch.AdvanceWorkflow(ctx, job.WorkflowID, step.StepIndex, result.Text)
}

// handleFailure decides whether to retry or fail the step/workflow.
func (w *Worker) handleFailure(ctx context.Context, job *models.JobMessage, step *models.WorkflowStep, cause error) error {
	w.logger.Warn().Err(cause).Str("step_id", step.ID).Int("retry", job.RetryCount).Msg("step execution failed")

	if job.RetryCount >= w.maxRetries {
		// Exhausted retries — fail step and workflow.
		_ = w.store.UpdateStepStatus(ctx, step.ID, models.StepStatusFailed, "", cause.Error(), job.RetryCount)
		_ = w.store.UpdateWorkflowStatus(ctx, job.WorkflowID, models.WorkflowStatusFailed, "", cause.Error())
		_ = w.queue.SendToDeadLetter(ctx, job)
		w.logger.Error().Str("step_id", step.ID).Msg("step sent to dead-letter queue")
		return nil
	}

	// Schedule exponential backoff retry.
	delay := backoffDelay(job.RetryCount)
	retryJob := &models.JobMessage{
		WorkflowID: job.WorkflowID,
		StepID:     job.StepID,
		RetryCount: job.RetryCount + 1,
	}
	if err := w.queue.EnqueueRetry(ctx, retryJob, delay); err != nil {
		return fmt.Errorf("enqueue retry: %w", err)
	}

	// Reset step to pending so it can be picked up cleanly.
	_ = w.store.UpdateStepStatus(ctx, step.ID, models.StepStatusPending, "", cause.Error(), retryJob.RetryCount)

	w.logger.Info().
		Str("step_id", step.ID).
		Dur("delay", delay).
		Int("retry_count", retryJob.RetryCount).
		Msg("step scheduled for retry")

	return nil
}

// backoffDelay returns exponential backoff: 2^attempt seconds, capped at 5 minutes.
func backoffDelay(attempt int) time.Duration {
	seconds := math.Pow(2, float64(attempt))
	if seconds > 300 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}

// Pool manages a set of concurrent workers.
type Pool struct {
	workers []*Worker
}

// NewPool creates `concurrency` workers sharing the same dependencies.
func NewPool(concurrency int, s *store.Store, q *queue.Queue, l *llm.Client, o *orchestrator.Orchestrator, maxRetries int, log zerolog.Logger) *Pool {
	workers := make([]*Worker, concurrency)
	for i := 0; i < concurrency; i++ {
		workers[i] = New(i+1, s, q, l, o, maxRetries, log)
	}
	return &Pool{workers: workers}
}

// Start launches all workers as goroutines.
func (p *Pool) Start(ctx context.Context) {
	for _, w := range p.workers {
		go w.Run(ctx)
	}
}
