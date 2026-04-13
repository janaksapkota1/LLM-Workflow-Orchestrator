package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"llm-orchestrator/internal/models"
	"llm-orchestrator/internal/orchestrator"
	"llm-orchestrator/internal/queue"
	"llm-orchestrator/internal/store"
)

// Handler holds dependencies for HTTP route handlers.
type Handler struct {
	store  *store.Store
	orch   *orchestrator.Orchestrator
	queue  *queue.Queue
	logger zerolog.Logger
}

// New creates an API Handler.
func New(s *store.Store, o *orchestrator.Orchestrator, q *queue.Queue, log zerolog.Logger) *Handler {
	return &Handler{store: s, orch: o, queue: q, logger: log}
}

// Routes returns the chi router with all routes registered.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(h.requestLogger)
	r.Use(jsonContentType)

	r.Get("/health", h.handleHealth)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/workflows", h.handleCreateWorkflow)
		r.Get("/workflows", h.handleListWorkflows)
		r.Get("/workflows/{id}", h.handleGetWorkflow)
		r.Get("/workflows/{id}/steps", h.handleGetWorkflowSteps)
		r.Delete("/workflows/{id}", h.handleCancelWorkflow)

		r.Get("/queue/depth", h.handleQueueDepth)
	})

	return r
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// POST /api/v1/workflows
func (h *Handler) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	var req models.CreateWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Task == "" {
		writeError(w, http.StatusBadRequest, "task is required")
		return
	}

	wf, err := h.orch.CreateWorkflow(r.Context(), req.Task, req.Metadata)
	if err != nil {
		h.logger.Error().Err(err).Msg("create workflow")
		writeError(w, http.StatusInternalServerError, "failed to create workflow")
		return
	}

	writeJSON(w, http.StatusCreated, wf)
}

// GET /api/v1/workflows
func (h *Handler) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 20)
	offset := queryInt(r, "offset", 0)

	workflows, err := h.store.ListWorkflows(r.Context(), limit, offset)
	if err != nil {
		h.logger.Error().Err(err).Msg("list workflows")
		writeError(w, http.StatusInternalServerError, "failed to list workflows")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"workflows": workflows,
		"limit":     limit,
		"offset":    offset,
	})
}

// GET /api/v1/workflows/{id}
func (h *Handler) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	wf, err := h.store.GetWorkflow(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "workflow not found")
		return
	}

	steps, err := h.store.GetStepsByWorkflow(r.Context(), id)
	if err != nil {
		h.logger.Error().Err(err).Msg("get steps")
		writeError(w, http.StatusInternalServerError, "failed to load steps")
		return
	}

	writeJSON(w, http.StatusOK, models.WorkflowResponse{Workflow: wf, Steps: steps})
}

// GET /api/v1/workflows/{id}/steps
func (h *Handler) handleGetWorkflowSteps(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	steps, err := h.store.GetStepsByWorkflow(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load steps")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"steps": steps})
}

// DELETE /api/v1/workflows/{id}
func (h *Handler) handleCancelWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.UpdateWorkflowStatus(r.Context(), id, models.WorkflowStatusCancelled, "", "cancelled by user"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel workflow")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// GET /api/v1/queue/depth
func (h *Handler) handleQueueDepth(w http.ResponseWriter, r *http.Request) {
	depth, err := h.queue.QueueDepth(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get queue depth")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"depth": depth})
}

// ── Middleware ────────────────────────────────────────────────────────────────

func (h *Handler) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.logger.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Str("remote", r.RemoteAddr).
			Msg("request")
		next.ServeHTTP(w, r)
	})
}

func jsonContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func queryInt(r *http.Request, key string, fallback int) int {
	if s := r.URL.Query().Get(key); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return fallback
}