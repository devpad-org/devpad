package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/project"
)

// Settings keeps a project's pipeline setting, for the user whose project
// it is.
type Settings interface {
	SetPipelineConfig(ctx context.Context, userID, projectID int64, config string) (*project.Project, error)
}

// Handler holds the HTTP handlers for a project's pipeline setting and its
// pipelines.
type Handler struct {
	settings  Settings
	pipelines Service
}

// NewHandler creates a Handler.
func NewHandler(settings Settings, pipelines Service) *Handler {
	return &Handler{settings: settings, pipelines: pipelines}
}

// HandleSetConfig sets the pipeline a project's commits without a pipeline
// file run, refusing YAML that does not parse. Blank clears it. It answers
// with the project, as the project's other settings do.
func (h *Handler) HandleSetConfig(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Config string `json:"config"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := ValidateSetting(req.Config); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config := req.Config
	if strings.TrimSpace(config) == "" {
		config = ""
	}
	p, err := h.settings.SetPipelineConfig(r.Context(), user.ID, projectID, config)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project.Response(p)})
}

// HandleList lists a project's pipelines, newest first, optionally only a
// branch's or a merge request's, and paged back with before.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	q := r.URL.Query()
	f := ListFilter{Branch: q.Get("branch")}
	for name, dst := range map[string]*int64{"mergeRequestId": &f.MergeRequestID, "before": &f.Before} {
		if v := q.Get(name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "invalid "+name)
				return
			}
			*dst = n
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxListLimit {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and "+strconv.Itoa(MaxListLimit))
			return
		}
		f.Limit = n
	}
	pipelines, err := h.pipelines.List(r.Context(), user.ID, projectID, f)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(pipelines))
	for _, p := range pipelines {
		items = append(items, pipelineResponse(p, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"pipelines": items})
}

// HandleLatest returns the newest pipeline of each branch (branch=, as
// often as needed) and merge request (mergeRequestId=) asked for that has
// one, for the badges beside them.
func (h *Handler) HandleLatest(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	q := r.URL.Query()
	f := LatestFilter{Branches: q["branch"]}
	for _, v := range q["mergeRequestId"] {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid mergeRequestId")
			return
		}
		f.MergeRequestIDs = append(f.MergeRequestIDs, id)
	}
	pipelines, err := h.pipelines.Latest(r.Context(), user.ID, projectID, f)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(pipelines))
	for _, p := range pipelines {
		items = append(items, pipelineResponse(p, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"pipelines": items})
}

// HandleStart starts a pipeline of a protected branch's current commit, or
// of an open merge request. It answers 202 with a new pipeline, or 200 with
// one already queued or running that tests the same.
func (h *Handler) HandleStart(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Branch         string `json:"branch"`
		MergeRequestID int64  `json:"mergeRequestId"`
	}
	if !decode(w, r, &req) {
		return
	}
	p, created, err := h.pipelines.Start(r.Context(), StartInput{
		UserID: user.ID, ProjectID: projectID, Branch: req.Branch, MergeRequestID: req.MergeRequestID, Trigger: TriggerUser,
	})
	if err != nil {
		handleServiceError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, map[string]any{"pipeline": pipelineResponse(p, true)})
}

// HandleGet returns a pipeline with its jobs and their scripts.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	user, projectID, pipelineID, ok := params(w, r)
	if !ok {
		return
	}
	p, err := h.pipelines.Get(r.Context(), user.ID, projectID, pipelineID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pipeline": pipelineResponse(p, true)})
}

// HandleCancel stops a queued or running pipeline.
func (h *Handler) HandleCancel(w http.ResponseWriter, r *http.Request) {
	user, projectID, pipelineID, ok := params(w, r)
	if !ok {
		return
	}
	p, err := h.pipelines.Cancel(r.Context(), user.ID, projectID, pipelineID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pipeline": pipelineResponse(p, true)})
}

// HandleJobOutput returns a job's output from the offset asked for, with
// all devpad's notes on the job so far, which are never part of the output:
// a client polls it, asking each time from the next offset it was given
// (never offset plus the length of data, which is sent as a JSON string, in
// which bytes that are not UTF-8 become U+FFFD), until it is told the job is
// done.
func (h *Handler) HandleJobOutput(w http.ResponseWriter, r *http.Request) {
	user, projectID, pipelineID, ok := params(w, r)
	if !ok {
		return
	}
	jobID, ok := pathID(w, r, "jobId")
	if !ok {
		return
	}
	var offset int64
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid offset")
			return
		}
		offset = n
	}
	out, err := h.pipelines.JobOutput(r.Context(), user.ID, projectID, pipelineID, OutputRequest{JobID: jobID, Offset: offset})
	if err != nil {
		handleServiceError(w, err)
		return
	}
	notes := out.Notes
	if notes == nil {
		notes = []Note{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"offset":  out.Offset,
		"data":    string(out.Data),
		"next":    out.Next,
		"done":    out.Done,
		"dropped": out.Dropped,
		"notes":   notes,
	})
}

func params(w http.ResponseWriter, r *http.Request) (*auth.User, int64, int64, bool) {
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return nil, 0, 0, false
	}
	pipelineID, ok := pathID(w, r, "pipelineId")
	if !ok {
		return nil, 0, 0, false
	}
	return auth.UserFromContext(r.Context()), projectID, pipelineID, true
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
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrJobNotFound), errors.Is(err, ErrMergeRequestNotFound), errors.Is(err, project.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, project.ErrForbidden):
		writeError(w, http.StatusForbidden, "access denied")
	case errors.Is(err, ErrInvalidInput), errors.Is(err, project.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrInvalidState), errors.Is(err, project.ErrNotReady), errors.Is(err, project.ErrDeleting):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrShuttingDown):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.As(err, &agentErr):
		project.WriteAgentError(w, agentErr)
	default:
		log.Printf("pipeline handler: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

// pipelineResponse is a pipeline as the API returns it, its jobs' scripts
// included or not.
func pipelineResponse(p *Pipeline, withScripts bool) map[string]any {
	jobs := make([]map[string]any, 0, len(p.Jobs))
	for _, j := range p.Jobs {
		jobs = append(jobs, jobResponse(j, withScripts))
	}
	paths := p.ConflictPaths
	if paths == nil {
		paths = []string{}
	}
	return map[string]any{
		"id":             p.ID,
		"projectId":      p.ProjectID,
		"kind":           p.Kind,
		"branch":         p.Branch,
		"mergeRequestId": p.MergeRequestID,
		"sourceBranch":   p.SourceBranch,
		"targetBranch":   p.TargetBranch,
		"headSha":        p.HeadSHA,
		"targetSha":      p.TargetSHA,
		"testedSha":      p.TestedSHA,
		// Where the jobs came from: a file at the commit read (the
		// branch's, or a merge request's target's), the project's setting,
		// or none.
		"configSource":  p.ConfigSource,
		"configPath":    p.ConfigPath,
		"configHash":    p.ConfigHash,
		"image":         p.Image,
		"status":        p.Status,
		"error":         p.Error,
		"conflictPaths": paths,
		"trigger":       p.Trigger,
		"rerunOf":       p.RerunOf,
		"createdAt":     p.CreatedAt,
		"startedAt":     p.StartedAt,
		"finishedAt":    p.FinishedAt,
		"jobs":          jobs,
	}
}

func jobResponse(j *JobRun, withScript bool) map[string]any {
	services := j.Services
	if services == nil {
		services = []string{}
	}
	resp := map[string]any{
		"id":        j.ID,
		"name":      j.Name,
		"position":  j.Position,
		"timeoutMs": j.Timeout.Milliseconds(),
		"services":  services,
		"status":    j.Status,
		"exitCode":  j.ExitCode,
		"error":     j.Error,
		// How much the job printed, how much of the end of it is kept,
		// and whether that is still there.
		"outputBytes": j.OutputBytes,
		"keptBytes":   j.KeptBytes,
		"outputKept":  j.OutputKept(),
		"startedAt":   j.StartedAt,
		"finishedAt":  j.FinishedAt,
	}
	if withScript {
		resp["run"] = j.Run
	}
	return resp
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
