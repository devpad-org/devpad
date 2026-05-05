package workspace

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
)

// HandleGitStatus returns the git status of a workspace.
func (h *Handler) HandleGitStatus(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	status, err := h.service.GitStatus(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, status)
}

// HandleGitLog returns the commit log of a workspace.
func (h *Handler) HandleGitLog(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	count := 50
	if c := r.URL.Query().Get("count"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 && n <= 200 {
			count = n
		}
	}

	commits, err := h.service.GitLog(r.Context(), user.ID, id, count, agent.GitLogOptions{
		AllBranches: r.URL.Query().Get("all") == "true",
	})
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"commits": commits})
}

// HandleGitCommitFiles returns the files changed by a commit.
func (h *Handler) HandleGitCommitFiles(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	commit := r.URL.Query().Get("commit")
	if commit == "" {
		writeError(w, http.StatusBadRequest, "commit is required")
		return
	}

	files, err := h.service.GitCommitFiles(r.Context(), user.ID, id, commit)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// HandleGitCommitFileDiff returns before/after contents for a file changed by a commit.
func (h *Handler) HandleGitCommitFileDiff(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	commit := r.URL.Query().Get("commit")
	path := r.URL.Query().Get("path")
	oldPath := r.URL.Query().Get("oldPath")
	if commit == "" {
		writeError(w, http.StatusBadRequest, "commit is required")
		return
	}
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	diff, err := h.service.GitCommitFileDiff(r.Context(), user.ID, id, commit, path, oldPath)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, diff)
}

// HandleGitBranches returns the branches of a workspace.
func (h *Handler) HandleGitBranches(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	branches, err := h.service.GitBranches(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, branches)
}

// HandleGitDiff returns the diff for a workspace or specific file.
func (h *Handler) HandleGitDiff(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	path := r.URL.Query().Get("path")
	staged := r.URL.Query().Get("staged") == "true"

	diff, err := h.service.GitDiff(r.Context(), user.ID, id, path, staged)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"diff": diff})
}

// HandleGitRemotes returns the list of remotes with their URLs.
func (h *Handler) HandleGitRemotes(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	remotes, err := h.service.GitRemotes(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"remotes": remotes})
}

// HandleGitAction performs a git action (stage, commit, push, etc).
func (h *Handler) HandleGitAction(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	var req GitActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Action == "" {
		writeError(w, http.StatusBadRequest, "action is required")
		return
	}

	result, err := h.service.GitAction(r.Context(), user.ID, id, req)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}
