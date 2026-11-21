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
	links, err := h.service.Links(r.Context(), user.ID, projectID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, taskResponse(t, links[t.ID]))
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
		Title      string   `json:"title"`
		Body       string   `json:"body"`
		Start      bool     `json:"start"`
		BaseBranch string   `json:"baseBranch"`
		BudgetUSD  *float64 `json:"budgetUsd"`
		BlockedBy  []int64  `json:"blockedBy"`
		Related    []int64  `json:"related"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	t, err := h.service.Create(r.Context(), user.ID, projectID, CreateInput{
		Title: req.Title, Body: req.Body, AgentSettings: req.settings(), Start: req.Start, BaseBranch: req.BaseBranch,
		BudgetUSD: req.BudgetUSD, BlockedBy: req.BlockedBy, Related: req.Related,
	})
	if err != nil {
		handleServiceError(w, err)
		return
	}
	h.writeTask(w, r, http.StatusCreated, user.ID, t)
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
	h.writeTask(w, r, http.StatusCreated, user.ID, t)
}

// HandleGet returns one task.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	h.withTask(w, r, func(userID, projectID, taskID int64) (*Task, error) {
		return h.service.Get(r.Context(), userID, projectID, taskID)
	})
}

// HandleUpdate edits a task's title, body and budget.
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title     string   `json:"title"`
		Body      string   `json:"body"`
		BudgetUSD *float64 `json:"budgetUsd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.withTask(w, r, func(userID, projectID, taskID int64) (*Task, error) {
		return h.service.Update(r.Context(), userID, projectID, taskID, UpdateInput(req))
	})
}

// HandleSetBudget replaces a task's budget alone, as raising it to let its
// agents continue does.
func (h *Handler) HandleSetBudget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BudgetUSD *float64 `json:"budgetUsd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BudgetUSD == nil {
		writeError(w, http.StatusBadRequest, "budgetUsd is required")
		return
	}
	h.withTask(w, r, func(userID, projectID, taskID int64) (*Task, error) {
		return h.service.SetBudget(r.Context(), userID, projectID, taskID, *req.BudgetUSD)
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

// HandleCancelStart drops a blocked task's request to start when unblocked.
func (h *Handler) HandleCancelStart(w http.ResponseWriter, r *http.Request) {
	h.withTask(w, r, func(userID, projectID, taskID int64) (*Task, error) {
		return h.service.CancelStart(r.Context(), userID, projectID, taskID)
	})
}

// HandleLink links a task to another of the project's tasks.
func (h *Handler) HandleLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind   LinkKind `json:"kind"`
		TaskID int64    `json:"taskId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.withTask(w, r, func(userID, projectID, taskID int64) (*Task, error) {
		return h.service.Link(r.Context(), userID, projectID, taskID, req.Kind, req.TaskID)
	})
}

// HandleUnlink removes the link between two tasks.
func (h *Handler) HandleUnlink(w http.ResponseWriter, r *http.Request) {
	otherID, ok := pathID(w, r, "otherId")
	if !ok {
		return
	}
	h.withTask(w, r, func(userID, projectID, taskID int64) (*Task, error) {
		return h.service.Unlink(r.Context(), userID, projectID, taskID, otherID)
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

// HandleListComments returns a task's comments, oldest first.
func (h *Handler) HandleListComments(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, taskID, ok := taskPath(w, r)
	if !ok {
		return
	}
	comments, err := h.service.Comments(r.Context(), user.ID, projectID, taskID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(comments))
	for _, c := range comments {
		items = append(items, commentResponse(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": items})
}

// HandleAddComment leaves the user's comment on a task.
func (h *Handler) HandleAddComment(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, taskID, ok := taskPath(w, r)
	if !ok {
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c, err := h.service.AddComment(r.Context(), user.ID, projectID, taskID, AuthorUser, req.Body)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"comment": commentResponse(c)})
}

// HandleEditComment replaces the body of one of the user's comments on a
// task, keeping the earlier versions.
func (h *Handler) HandleEditComment(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, taskID, ok := taskPath(w, r)
	if !ok {
		return
	}
	commentID, ok := pathID(w, r, "commentId")
	if !ok {
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c, err := h.service.EditComment(r.Context(), user.ID, projectID, taskID, commentID, AuthorUser, req.Body)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comment": commentResponse(c)})
}

// HandleCommentRevisions returns every version of a comment on a task,
// oldest first.
func (h *Handler) HandleCommentRevisions(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, taskID, ok := taskPath(w, r)
	if !ok {
		return
	}
	commentID, ok := pathID(w, r, "commentId")
	if !ok {
		return
	}
	revisions, err := h.service.CommentRevisions(r.Context(), user.ID, projectID, taskID, commentID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(revisions))
	for _, v := range revisions {
		items = append(items, map[string]any{"id": v.ID, "body": v.Body, "createdAt": v.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": items})
}

func commentResponse(c *Comment) map[string]any {
	return map[string]any{
		"id":        c.ID,
		"taskId":    c.TaskID,
		"author":    c.Author,
		"body":      c.Body,
		"revisions": c.Revisions,
		"createdAt": c.CreatedAt,
		"updatedAt": c.UpdatedAt,
	}
}

// taskPath reads the project and task ids from the path.
func taskPath(w http.ResponseWriter, r *http.Request) (projectID, taskID int64, ok bool) {
	if projectID, ok = pathID(w, r, "id"); !ok {
		return 0, 0, false
	}
	if taskID, ok = pathID(w, r, "taskId"); !ok {
		return 0, 0, false
	}
	return projectID, taskID, true
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
	h.writeTask(w, r, http.StatusOK, user.ID, t)
}

// writeTask writes a task with its links.
func (h *Handler) writeTask(w http.ResponseWriter, r *http.Request, status int, userID int64, t *Task) {
	links, err := h.service.Links(r.Context(), userID, t.ProjectID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, status, map[string]any{"task": taskResponse(t, links[t.ID])})
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
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrCommentNotFound), errors.Is(err, project.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, project.ErrForbidden):
		writeError(w, http.StatusForbidden, "access denied")
	case errors.Is(err, ErrNotCommentAuthor):
		writeError(w, http.StatusForbidden, "you can only edit your own comments")
	case errors.Is(err, ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrInvalidState), errors.Is(err, ErrAlreadyLinked), errors.Is(err, ErrCommentLimit), errors.Is(err, project.ErrNotReady), errors.Is(err, project.ErrNoWorkspace):
		writeError(w, http.StatusConflict, err.Error())
	default:
		log.Printf("task handler: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func taskResponse(t *Task, links Links) map[string]any {
	return map[string]any{
		"blockedBy":          ids(links.BlockedBy),
		"blocks":             ids(links.Blocks),
		"related":            ids(links.Related),
		"blocked":            links.Blocked,
		"startWhenUnblocked": t.StartWhenUnblocked,
		"id":                 t.ID,
		"projectId":          t.ProjectID,
		"title":              t.Title,
		"body":               t.Body,
		"status":             t.Status,
		"branch":             t.Branch,
		"workspaceId":        t.WorkspaceID,
		"conversationId":     t.ConversationID,
		"runId":              t.RunID,
		"agentId":            t.AgentID,
		"model":              t.Model,
		"effort":             t.Effort,
		"pendingMessage":     t.PendingMessage,
		"headSha":            t.HeadSHA,
		"baseBranch":         t.BaseBranch,
		"handedOff":          t.Handoff != "",
		"budgetUsd":          t.BudgetUSD,
		"costUsd":            t.CostUSD,
		"error":              t.Error,
		"createdAt":          t.CreatedAt,
		"updatedAt":          t.UpdatedAt,
	}
}

// ids is a list of ids for JSON, never null.
func ids(list []int64) []int64 {
	if list == nil {
		return []int64{}
	}
	return list
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
