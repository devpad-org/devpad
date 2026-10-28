package mergerequest

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/task"
)

// Handler holds HTTP handlers for merge request endpoints.
type Handler struct {
	service Service
}

// NewHandler creates a new merge request Handler.
func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

// HandleList returns a project's merge requests.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	mrs, err := h.service.List(r.Context(), user.ID, projectID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(mrs))
	for _, mr := range mrs {
		items = append(items, mergeRequestResponse(mr))
	}
	writeJSON(w, http.StatusOK, map[string]any{"mergeRequests": items})
}

// HandleCreate opens a merge request for a published branch.
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Title        string `json:"title"`
		Description  string `json:"description"`
		SourceBranch string `json:"sourceBranch"`
		TargetBranch string `json:"targetBranch"`
	}
	if !decode(w, r, &req) {
		return
	}
	mr, err := h.service.Create(r.Context(), user.ID, projectID, CreateInput(req))
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"mergeRequest": mergeRequestResponse(mr)})
}

// HandleGet returns a merge request with its reviews and comments.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	user, projectID, mrID, ok := params(w, r)
	if !ok {
		return
	}
	d, err := h.service.Get(r.Context(), user.ID, projectID, mrID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	reviews := make([]map[string]any, 0, len(d.Reviews))
	for _, rv := range d.Reviews {
		reviews = append(reviews, reviewResponse(rv))
	}
	comments := make([]map[string]any, 0, len(d.Comments))
	for _, c := range d.Comments {
		comments = append(comments, commentResponse(c))
	}
	body := mergeRequestResponse(d.MergeRequest)
	pushes := make([]map[string]any, 0, len(d.Pushes))
	for _, p := range d.Pushes {
		pushes = append(pushes, pushResponse(p))
	}
	body["reviews"], body["comments"], body["pushes"] = reviews, comments, pushes
	body["ahead"], body["behind"] = d.Ahead, d.Behind
	body["task"] = nil
	if d.Task != nil {
		body["task"] = taskSummaryResponse(d.Task)
	}
	writeJSON(w, http.StatusOK, map[string]any{"mergeRequest": body})
}

// HandleDiff lists the files a merge request changes. File contents come
// from the project file endpoint at the returned mergeBase and headSha.
func (h *Handler) HandleDiff(w http.ResponseWriter, r *http.Request) {
	user, projectID, mrID, ok := params(w, r)
	if !ok {
		return
	}
	diff, err := h.service.Diff(r.Context(), user.ID, projectID, mrID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

// HandleCommits lists the commits a merge request proposes.
func (h *Handler) HandleCommits(w http.ResponseWriter, r *http.Request) {
	user, projectID, mrID, ok := params(w, r)
	if !ok {
		return
	}
	commits, err := h.service.Commits(r.Context(), user.ID, projectID, mrID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, commits)
}

// HandleReview records the user's review.
func (h *Handler) HandleReview(w http.ResponseWriter, r *http.Request) {
	user, projectID, mrID, ok := params(w, r)
	if !ok {
		return
	}
	var req struct {
		Verdict  Verdict `json:"verdict"`
		Body     string  `json:"body"`
		Comments []struct {
			Path string `json:"path"`
			Line int    `json:"line"`
			Body string `json:"body"`
		} `json:"comments"`
		SendToAgent bool `json:"sendToAgent"`
	}
	if !decode(w, r, &req) {
		return
	}
	in := ReviewInput{Verdict: req.Verdict, Body: req.Body, SendToAgent: req.SendToAgent}
	for _, c := range req.Comments {
		in.Comments = append(in.Comments, CommentInput(c))
	}
	rv, err := h.service.Review(r.Context(), user.ID, projectID, mrID, in)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"review": reviewResponse(rv)})
}

// HandleComment leaves the user's comment at once, outside a review: a
// reply in a thread, or a new thread on a line or on the whole change.
func (h *Handler) HandleComment(w http.ResponseWriter, r *http.Request) {
	user, projectID, mrID, ok := params(w, r)
	if !ok {
		return
	}
	var req struct {
		ReplyTo int64  `json:"replyTo"`
		Path    string `json:"path"`
		Line    int    `json:"line"`
		Body    string `json:"body"`
	}
	if !decode(w, r, &req) {
		return
	}
	c, err := h.service.Comment(r.Context(), user.ID, projectID, mrID, UserCommentInput(req))
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"comment": commentResponse(c)})
}

// HandleResolveThread resolves the thread a comment is in, or opens it
// again, and returns the thread's first comment.
func (h *Handler) HandleResolveThread(w http.ResponseWriter, r *http.Request) {
	user, projectID, mrID, ok := params(w, r)
	if !ok {
		return
	}
	commentID, ok := pathID(w, r, "commentId")
	if !ok {
		return
	}
	var req struct {
		Resolved bool `json:"resolved"`
	}
	if !decode(w, r, &req) {
		return
	}
	c, err := h.service.ResolveThread(r.Context(), user.ID, projectID, mrID, commentID, req.Resolved)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comment": commentResponse(c)})
}

// HandleAgentReview starts a review agent.
func (h *Handler) HandleAgentReview(w http.ResponseWriter, r *http.Request) {
	user, projectID, mrID, ok := params(w, r)
	if !ok {
		return
	}
	var req struct {
		AgentID string `json:"agentId"`
		Model   string `json:"model"`
		Effort  string `json:"effort"`
		AutoFix bool   `json:"autoFix"`
	}
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	rv, err := h.service.StartAgentReview(r.Context(), user.ID, projectID, mrID, AgentReviewInput(req))
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"review": reviewResponse(rv)})
}

// HandleMerge merges a merge request into its target branch.
func (h *Handler) HandleMerge(w http.ResponseWriter, r *http.Request) {
	user, projectID, mrID, ok := params(w, r)
	if !ok {
		return
	}
	var req struct {
		Strategy string `json:"strategy"`
		Message  string `json:"message"`
		Push     bool   `json:"push"`
	}
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	result, err := h.service.Merge(r.Context(), user.ID, projectID, mrID, MergeInput(req))
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mergeRequest": mergeRequestResponse(result.MergeRequest),
		"pushed":       result.Pushed,
		"pushError":    result.PushError,
	})
}

// HandleClose closes a merge request without merging it.
func (h *Handler) HandleClose(w http.ResponseWriter, r *http.Request) {
	user, projectID, mrID, ok := params(w, r)
	if !ok {
		return
	}
	mr, err := h.service.Close(r.Context(), user.ID, projectID, mrID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mergeRequest": mergeRequestResponse(mr)})
}

// HandleResolveConflicts sends merge conflicts to the task's agent.
func (h *Handler) HandleResolveConflicts(w http.ResponseWriter, r *http.Request) {
	user, projectID, mrID, ok := params(w, r)
	if !ok {
		return
	}
	var req struct {
		Files []string `json:"files"`
	}
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	if err := h.service.ResolveConflicts(r.Context(), user.ID, projectID, mrID, req.Files); err != nil {
		handleServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleSendBack starts the task's agent again on the merge request's
// feedback.
func (h *Handler) HandleSendBack(w http.ResponseWriter, r *http.Request) {
	user, projectID, mrID, ok := params(w, r)
	if !ok {
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	t, err := h.service.SendBack(r.Context(), user.ID, projectID, mrID, SendBackInput(req))
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"task": taskSummaryResponse(t)})
}

func params(w http.ResponseWriter, r *http.Request) (*auth.User, int64, int64, bool) {
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return nil, 0, 0, false
	}
	mrID, ok := pathID(w, r, "mrId")
	if !ok {
		return nil, 0, 0, false
	}
	return auth.UserFromContext(r.Context()), projectID, mrID, true
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return 0, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func handleServiceError(w http.ResponseWriter, err error) {
	var agentErr *agent.AgentError
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, project.ErrNotFound), errors.Is(err, task.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, project.ErrForbidden):
		writeError(w, http.StatusForbidden, "access denied")
	case errors.Is(err, ErrInvalidInput), errors.Is(err, project.ErrInvalidInput), errors.Is(err, task.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrInvalidState), errors.Is(err, task.ErrInvalidState),
		errors.Is(err, project.ErrNotReady), errors.Is(err, project.ErrSandboxLimit),
		errors.Is(err, project.ErrBranchInUse), errors.Is(err, project.ErrBranchPublishing), errors.Is(err, project.ErrDeleting):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &agentErr):
		project.WriteAgentError(w, agentErr)
	default:
		log.Printf("merge request handler: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func mergeRequestResponse(mr *MergeRequest) map[string]any {
	return map[string]any{
		"id":             mr.ID,
		"projectId":      mr.ProjectID,
		"taskId":         mr.TaskID,
		"title":          mr.Title,
		"description":    mr.Description,
		"sourceBranch":   mr.SourceBranch,
		"targetBranch":   mr.TargetBranch,
		"status":         mr.Status,
		"headSha":        mr.HeadSHA,
		"baseSha":        mr.BaseSHA,
		"mergeSha":       mr.MergeSHA,
		"mergedAt":       mr.MergedAt,
		"createdAt":      mr.CreatedAt,
		"updatedAt":      mr.UpdatedAt,
		"agentReviewing": mr.AgentReviewing,
	}
}

func reviewResponse(rv *Review) map[string]any {
	return map[string]any{
		"id":          rv.ID,
		"author":      rv.Author,
		"status":      rv.Status,
		"verdict":     rv.Verdict,
		"body":        rv.Body,
		"headSha":     rv.HeadSHA,
		"workspaceId": rv.WorkspaceID,
		"runId":       rv.RunID,
		"agentId":     rv.AgentID,
		"model":       rv.Model,
		"effort":      rv.Effort,
		"autoFix":     rv.AutoFix,
		"error":       rv.Error,
		"createdAt":   rv.CreatedAt,
		"updatedAt":   rv.UpdatedAt,
	}
}

func commentResponse(c *Comment) map[string]any {
	return map[string]any{
		"id":         c.ID,
		"reviewId":   c.ReviewID,
		"replyTo":    c.ReplyTo,
		"author":     c.Author,
		"path":       c.Path,
		"line":       c.Line,
		"body":       c.Body,
		"headSha":    c.HeadSHA,
		"createdAt":  c.CreatedAt,
		"resolvedBy": c.ResolvedBy,
		"resolvedAt": c.ResolvedAt,
	}
}

func pushResponse(p *Push) map[string]any {
	return map[string]any{
		"id":        p.ID,
		"source":    p.Source,
		"beforeSha": p.BeforeSHA,
		"headSha":   p.HeadSHA,
		"createdAt": p.CreatedAt,
	}
}

// taskSummaryResponse is what a merge request shows of its task.
func taskSummaryResponse(t *task.Task) map[string]any {
	return map[string]any{
		"id":          t.ID,
		"status":      t.Status,
		"workspaceId": t.WorkspaceID,
		"error":       t.Error,
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
