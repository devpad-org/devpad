package task

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/project"
)

// Handler holds HTTP handlers for task endpoints.
type Handler struct {
	service Service
}

// NewHandler creates a new task Handler.
func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

type agentSettingsRequest struct {
	AgentID string `json:"agentId"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

func (r agentSettingsRequest) settings() AgentSettings {
	return AgentSettings{AgentID: r.AgentID, Model: r.Model, Effort: r.Effort}
}

// HandleList returns a project's tasks.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	tasks, err := h.service.List(r.Context(), user.ID, projectID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, taskResponse(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": items})
}

// HandleCreate creates a task, optionally starting an agent on it.
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		agentSettingsRequest
		Title      string `json:"title"`
		Body       string `json:"body"`
		Start      bool   `json:"start"`
		BaseBranch string `json:"baseBranch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	t, err := h.service.Create(r.Context(), user.ID, projectID, CreateInput{
		Title: req.Title, Body: req.Body, AgentSettings: req.settings(), Start: req.Start, BaseBranch: req.BaseBranch,
	})
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"task": taskResponse(t)})
}

// HandleHandoff hands a conversation from the project workspace off to a
// new task, which starts at once.
func (h *Handler) HandleHandoff(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		agentSettingsRequest
		ConversationID int64  `json:"conversationId"`
		Title          string `json:"title"`
		Instructions   string `json:"instructions"`
		BaseBranch     string `json:"baseBranch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	t, err := h.service.Handoff(r.Context(), user.ID, projectID, HandoffInput{
		ConversationID: req.ConversationID,
		Title:          req.Title,
		Instructions:   req.Instructions,
		BaseBranch:     req.BaseBranch,
		AgentSettings:  req.settings(),
	})
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"task": taskResponse(t)})
}

// HandleGet returns one task.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	h.withTask(w, r, func(userID, projectID, taskID int64) (*Task, error) {
		return h.service.Get(r.Context(), userID, projectID, taskID)
	})
}

// HandleUpdate edits a task's title and body.
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.withTask(w, r, func(userID, projectID, taskID int64) (*Task, error) {
		return h.service.Update(r.Context(), userID, projectID, taskID, UpdateInput(req))
	})
}

// HandleStart starts or continues an agent on a task.
func (h *Handler) HandleStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		agentSettingsRequest
		Message string `json:"message"`
	}
	// The body is optional: an empty one keeps the task's agent settings.
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	h.withTask(w, r, func(userID, projectID, taskID int64) (*Task, error) {
		return h.service.Start(r.Context(), userID, projectID, taskID, StartInput{
			AgentSettings: req.settings(), Message: req.Message,
		})
	})
}

// HandleCancel stops a task's agent, removes its sandbox, and closes it.
func (h *Handler) HandleCancel(w http.ResponseWriter, r *http.Request) {
	h.withTask(w, r, func(userID, projectID, taskID int64) (*Task, error) {
		return h.service.Cancel(r.Context(), userID, projectID, taskID)
	})
}

// HandleDelete removes a task and its sandbox.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	taskID, ok := pathID(w, r, "taskId")
	if !ok {
		return
	}
	if err := h.service.Delete(r.Context(), user.ID, projectID, taskID); err != nil {
		handleServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) withTask(w http.ResponseWriter, r *http.Request, fn func(userID, projectID, taskID int64) (*Task, error)) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	taskID, ok := pathID(w, r, "taskId")
	if !ok {
		return
	}
	t, err := fn(user.ID, projectID, taskID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": taskResponse(t)})
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return 0, false
	}
	return id, true
}

func handleServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, project.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, project.ErrForbidden):
		writeError(w, http.StatusForbidden, "access denied")
	case errors.Is(err, ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrInvalidState), errors.Is(err, project.ErrNotReady), errors.Is(err, project.ErrNoWorkspace):
		writeError(w, http.StatusConflict, err.Error())
	default:
		log.Printf("task handler: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func taskResponse(t *Task) map[string]any {
	return map[string]any{
		"id":             t.ID,
		"projectId":      t.ProjectID,
		"title":          t.Title,
		"body":           t.Body,
		"status":         t.Status,
		"branch":         t.Branch,
		"workspaceId":    t.WorkspaceID,
		"conversationId": t.ConversationID,
		"runId":          t.RunID,
		"agentId":        t.AgentID,
		"model":          t.Model,
		"effort":         t.Effort,
		"pendingMessage": t.PendingMessage,
		"headSha":        t.HeadSHA,
		"baseBranch":     t.BaseBranch,
		"handedOff":      t.Handoff != "",
		"error":          t.Error,
		"createdAt":      t.CreatedAt,
		"updatedAt":      t.UpdatedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
