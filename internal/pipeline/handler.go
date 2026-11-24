package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
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
// branch's or a merge request's, of one purpose, and paged back with before.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	q := r.URL.Query()
	f := ListFilter{Branch: q.Get("branch"), Purpose: Purpose(q.Get("purpose"))}
	if f.Purpose != "" && !slices.Contains(Purposes(), f.Purpose) {
		writeError(w, http.StatusBadRequest, "invalid purpose")
		return
	}
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

// HandleLatest says how each branch (branch=, as often as needed) asked
// for stands for pipelines, and returns the newest pipeline of each merge
// request (mergeRequestId=) asked for that has one, for the badges beside
// them.
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
	latest, err := h.pipelines.Latest(r.Context(), user.ID, projectID, f)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	mrs := make([]map[string]any, 0, len(latest.MergeRequests))
	for _, p := range latest.MergeRequests {
		mrs = append(mrs, pipelineResponse(p, false))
	}
	branches := make(map[string]any, len(latest.Branches))
	for name, st := range latest.Branches {
		var tip any
		if st.Tip != "" {
			tip = st.Tip
		}
		// The commit the branch is at now (null when the repo could not be
		// read), whether it runs pipelines by itself, whether a pipeline is
		// configured there (null when that could not be read), and how its
		// pipelines stand for it.
		branches[name] = withStanding(map[string]any{"tip": tip, "automatic": st.Automatic, "configured": st.Configured}, st.Standing)
	}
	writeJSON(w, http.StatusOK, map[string]any{"mergeRequests": mrs, "branches": branches})
}

// withStanding adds to resp how a subject's pipelines stand, as the app's
// PipelineStanding reads it: current, latest and noResult.
func withStanding(resp map[string]any, st Standing) map[string]any {
	resp["current"] = pipelineOrNil(st.Current)
	resp["latest"] = pipelineOrNil(st.Latest)
	resp["noResult"] = noResultResponse(st.NoResult)
	return resp
}

// noResultResponse is a NoResult as the app reads it, or null.
func noResultResponse(st *NoResult) any {
	if st == nil {
		return nil
	}
	return map[string]any{"pipeline": pipelineResponse(st.Pipeline, false), "retrying": st.Retrying, "reason": st.Reason}
}

// HandleMergeRequestStatus says how a merge request stands for pipelines
// now: whether a pipeline is configured, the pipeline that tested its head
// on its target's current tip with the jobs there now, and its newest
// pipeline that counts, which may be of older commits.
func (h *Handler) HandleMergeRequestStatus(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	mrID, ok := pathID(w, r, "mrId")
	if !ok {
		return
	}
	st, err := h.pipelines.MergeRequestStatus(r.Context(), user.ID, projectID, mrID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	// The proposed pipeline is information beside the merge request's own:
	// one that can't be read leaves the rest to show.
	proposed, err := h.pipelines.ProposedStatus(r.Context(), user.ID, projectID, st)
	if err != nil {
		// A failure is told once, not on every poll that repeats it.
		if !isRecentFailure(err) {
			log.Printf("pipeline handler: merge request %d's proposed pipeline: %v", mrID, err)
		}
		proposed = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": withStanding(map[string]any{
		"open":       st.Open,
		"headSha":    st.Key.HeadSHA,
		"targetSha":  st.Key.TargetSHA,
		"configHash": st.Key.ConfigHash,
		"source":     sourceOf(st),
		"configPath": st.ConfigPath,
		"invalid":    st.Invalid,
		"configured": st.Open && st.Configured(),
		"automatic":  st.Automatic,
		"gate":       gateResponse(st.Gate()),
		"onTarget":   targetResponse(st.OnTarget),
		// The pipeline the change proposes, run for information only; null
		// when the change leaves the pipeline file alone.
		"proposed": proposedResponse(proposed),
	}, st.Standing)})
}

func pipelineOrNil(p *Pipeline) any {
	if p == nil {
		return nil
	}
	return pipelineResponse(p, false)
}

// proposedResponse is the pipeline a merge request's change proposes, as
// the merge request page shows it apart from its own, or null.
func proposedResponse(st *ProposedStatus) any {
	if st == nil {
		return nil
	}
	return withStanding(map[string]any{
		"file":       st.File,
		"headSha":    st.Key.HeadSHA,
		"targetSha":  st.Key.TargetSHA,
		"configHash": st.Key.ConfigHash,
		"configPath": st.ConfigPath,
		"automatic":  st.Automatic,
		"conflict":   st.Conflict,
		"removed":    st.Removed,
		"sameJobs":   st.SameJobs,
		"invalid":    st.Invalid,
	}, st.Standing)
}

// targetResponse is how the target's own pipeline went on the commit the
// merge request's failed pipeline merged onto: which of its failing jobs
// fail there too, or that it is still to say. Null when the current
// pipeline didn't fail.
func targetResponse(t *TargetResult) any {
	if t == nil {
		return nil
	}
	var pipelineID any
	if t.Pipeline != nil {
		pipelineID = t.Pipeline.ID
	}
	failing := t.Failing()
	if failing == nil {
		failing = []string{}
	}
	return map[string]any{"branch": t.Branch, "pipelineId": pipelineID, "failing": failing, "pending": t.Pending}
}

// gateResponse is how the merge gate stands: what the merge card shows,
// and what a merge that overrides it names, the state, the pipeline and
// the commits, which must all still be so when it merges.
func gateResponse(g *Gate) map[string]any {
	var pipelineID any
	if g.Pipeline != nil {
		pipelineID = g.Pipeline.ID
	}
	return map[string]any{
		"state":       g.State,
		"guarded":     g.Guarded(),
		"clear":       g.Clear(),
		"waits":       g.Waits,
		"overridable": g.Overridable(),
		"reason":      g.Reason,
		"pipelineId":  pipelineID,
		"headSha":     g.HeadSHA,
		"targetSha":   g.TargetSHA,
	}
}

// sourceOf is where the jobs of the merge request's pipeline come from now,
// none for one that can no longer merge, which has no pipeline to run.
func sourceOf(st *MergeRequestStatus) Source {
	if !st.Open {
		return SourceNone
	}
	return st.Source
}

// HandleStart starts a pipeline of a protected branch's current commit, or
// of an open merge request, its own or, with purpose "proposed", the one
// its change proposes. It answers 202 with a new pipeline, or 200 with one
// already queued or running that tests the same.
func (h *Handler) HandleStart(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Branch         string  `json:"branch"`
		MergeRequestID int64   `json:"mergeRequestId"`
		Purpose        Purpose `json:"purpose"`
	}
	if !decode(w, r, &req) {
		return
	}
	p, created, err := h.pipelines.Start(r.Context(), StartInput{
		UserID: user.ID, ProjectID: projectID, Branch: req.Branch, MergeRequestID: req.MergeRequestID, Purpose: req.Purpose, Trigger: TriggerUser,
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
	writeJSON(w, http.StatusOK, map[string]any{"pipeline": h.withRunAgain(r.Context(), user.ID, p)})
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
	writeJSON(w, http.StatusOK, map[string]any{"pipeline": h.withRunAgain(r.Context(), user.ID, p)})
}

// withRunAgain is p as a single pipeline's read shows it, saying, once it
// has finished, whether Run again would start now (CanRunAgain), which
// the app offers only then. When devpad can't read whether it would, Run
// again is offered, and pressing it says what went wrong.
func (h *Handler) withRunAgain(ctx context.Context, userID int64, p *Pipeline) map[string]any {
	resp := pipelineResponse(p, true)
	if !p.Status.Finished() {
		resp["canRunAgain"] = false
		return resp
	}
	can, err := h.pipelines.CanRunAgain(ctx, userID, p)
	if err != nil {
		if !isRecentFailure(err) {
			log.Printf("pipeline handler: whether pipeline %d can run again: %v", p.ID, err)
		}
		can = true
	}
	resp["canRunAgain"] = can
	return resp
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
	// Kept bounded as the pin answered; bounded again however it was kept.
	paths := p.ConflictPaths[:min(len(p.ConflictPaths), agent.MaxConflictPaths)]
	if paths == nil {
		paths = []string{}
	}
	return map[string]any{
		"id":        p.ID,
		"projectId": p.ProjectID,
		"kind":      p.Kind,
		// official: the branch's or merge request's own pipeline; proposed:
		// the jobs a merge request's change proposes, run for information.
		"purpose":        p.Purpose,
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
		"configSha":     p.ConfigSHA,
		"image":         p.Image,
		"status":        p.Status,
		"error":         p.Error,
		"conflictPaths": paths,
		"conflictMore":  p.ConflictMore,
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
