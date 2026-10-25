package main

import (
	"context"
	"net/http"
	"strings"
)

type repoCheckoutResult struct {
	Branch      string `json:"branch"`
	SHA         string `json:"sha"`
	Upstream    string `json:"upstream"`
	UpstreamSHA string `json:"upstreamSha"`
	Created     bool   `json:"created"`
}

// handleRepoCheckout runs in a project workspace. It stores a bundle of a
// canonical branch as devpad/<branch>, creates the local branch from it if
// the workspace does not have one, and switches to the local branch. An
// existing local branch is never moved, so local commits survive; git switch
// refuses rather than overwrite uncommitted changes.
func handleRepoCheckout(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	branch := r.URL.Query().Get("branch")
	if err := validateBranchName(ctx, branch, "branch"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if gitExec(ctx, "rev-parse", "--git-dir") != nil {
		writeErr(w, http.StatusConflict, "workspace has no repository")
		return
	}
	bundle, cleanup, err := receiveBundle(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer cleanup()

	result := repoCheckoutResult{Branch: branch, Upstream: sandboxBaseRemote + "/" + branch}
	ref := "refs/remotes/" + result.Upstream
	if out, err := gitOutput(ctx, "fetch", "--no-tags", "--no-write-fetch-head", "--force", "--", bundle, "refs/heads/"+branch+":"+ref); err != nil {
		writeErr(w, http.StatusBadRequest, "fetching bundle: "+strings.TrimSpace(out))
		return
	}
	out, err := gitOutput(ctx, "rev-parse", ref)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "resolving "+ref)
		return
	}
	result.UpstreamSHA = strings.TrimSpace(out)

	if gitExec(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) != nil {
		if out, err := gitOutput(ctx, "branch", "--no-track", branch, ref); err != nil {
			writeErr(w, http.StatusInternalServerError, "creating branch: "+strings.TrimSpace(out))
			return
		}
		result.Created = true
	}
	if out, err := gitOutput(ctx, "switch", "--no-guess", branch); err != nil {
		writeErr(w, http.StatusConflict, "switching to "+branch+": "+strings.TrimSpace(out))
		return
	}

	out, err = gitOutput(ctx, "rev-parse", "HEAD")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "resolving HEAD: "+strings.TrimSpace(out))
		return
	}
	result.SHA = strings.TrimSpace(out)
	writeJSON(w, http.StatusOK, result)
}
