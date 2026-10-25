package main

import (
	"context"
	"fmt"
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

type repoBranchStatus struct {
	Branch      string `json:"branch"`
	SHA         string `json:"sha"`
	UpstreamSHA string `json:"upstreamSha,omitempty"`
	// Ahead counts local commits the project does not have, Behind the
	// project's commits the local branch lacks.
	Ahead  int `json:"ahead"`
	Behind int `json:"behind"`
}

// handleRepoBranchStatus compares a local branch of a project workspace with
// devpad/<branch>, the project's copy as of the last fetch. A branch the
// project does not have is compared with every devpad/* branch, so Ahead
// counts the commits that exist only here.
func handleRepoBranchStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
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
	sha, err := resolveCommit(ctx, "refs/heads/"+branch, branch, "branch")
	if err != nil {
		writeErr(w, http.StatusNotFound, "branch not found: "+branch)
		return
	}
	result := repoBranchStatus{Branch: branch, SHA: sha}
	upstream := "refs/remotes/" + sandboxBaseRemote + "/" + branch
	if up, err := gitOutput(ctx, "rev-parse", "--verify", "--quiet", upstream+"^{commit}"); err == nil {
		result.UpstreamSHA = strings.TrimSpace(up)
		out, err := gitOutput(ctx, "rev-list", "--left-right", "--count", sha+"..."+result.UpstreamSHA)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "comparing "+branch)
			return
		}
		if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d %d", &result.Ahead, &result.Behind); err != nil {
			writeErr(w, http.StatusInternalServerError, "parsing rev-list output")
			return
		}
	} else {
		out, err := gitOutput(ctx, "rev-list", "--count", sha, "--not", "--remotes="+sandboxBaseRemote)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "counting commits on "+branch)
			return
		}
		if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &result.Ahead); err != nil {
			writeErr(w, http.StatusInternalServerError, "parsing rev-list output")
			return
		}
	}
	writeJSON(w, http.StatusOK, result)
}
