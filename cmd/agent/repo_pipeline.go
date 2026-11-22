package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Pipeline endpoints run in a project's canonical bare repo. Devpad pins
// what a pipeline tests to a ref of its own, refs/devpad/pipelines/<id>,
// bundles it for the pipeline's jobs, and deletes it when the pipeline is
// done. Pinning never moves a branch, and nothing in the commits runs: no
// working tree is involved, hooks are off, and merges read no attributes
// from content (see mergeTree).

const pipelineRefPrefix = "refs/devpad/pipelines/"

// pipelineIDPattern matches the ids devpad gives pipelines.
var pipelineIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// noHooks turns hooks off for one git call, whatever the repo's config says:
// update-ref would run reference-transaction.
func noHooks(args ...string) []string {
	return append([]string{"-c", "core.hooksPath=/dev/null"}, args...)
}

type repoPipelinePin struct {
	ID     string `json:"id"`
	Commit string `json:"commit"`
	Source string `json:"source"`
	Target string `json:"target"`
}

type repoPipelineConflict struct {
	Message string   `json:"message"`
	Paths   []string `json:"paths,omitempty"`
}

type repoPipelineCommit struct {
	Ref      string                `json:"ref,omitempty"`
	SHA      string                `json:"sha,omitempty"`
	Conflict *repoPipelineConflict `json:"conflict,omitempty"`
}

// handleRepoPinPipeline pins what a pipeline tests, replacing an earlier pin
// of the same id: a commit as it is, or the trial merge of source onto
// target. Each is a full commit hash. A source that does not merge onto its
// target is an answer, not an error: it comes back as a conflict, with
// nothing pinned.
func handleRepoPinPipeline(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxGitRequestBody)
	var req repoPipelinePin
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !pipelineIDPattern.MatchString(req.ID) {
		writeErr(w, http.StatusBadRequest, "id must be lowercase letters, digits and hyphens")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	var sha string
	var conflict *repoPipelineConflict
	var err error
	switch {
	case req.Commit != "" && req.Source == "" && req.Target == "":
		sha, err = pipelineCommit(ctx, req.Commit, "commit")
	case req.Commit == "" && req.Source != "" && req.Target != "":
		sha, conflict, err = trialMerge(ctx, req.Source, req.Target)
	default:
		writeErr(w, http.StatusBadRequest, "pin a commit, or a source onto a target")
		return
	}
	if err != nil {
		writeRevError(w, err)
		return
	}
	if conflict != nil {
		writeJSON(w, http.StatusOK, repoPipelineCommit{Conflict: conflict})
		return
	}
	ref := pipelineRefPrefix + req.ID
	if out, err := gitOutput(ctx, noHooks("update-ref", ref, sha)...); err != nil {
		writeErr(w, http.StatusInternalServerError, "pinning "+ref+": "+strings.TrimSpace(out))
		return
	}
	writeJSON(w, http.StatusOK, repoPipelineCommit{Ref: ref, SHA: sha})
}

// pipelineCommit checks that hash is the full hash of a commit in the repo.
// A tag's is refused rather than followed, so what is pinned is what was
// named.
func pipelineCommit(ctx context.Context, hash, field string) (string, error) {
	if !isFullHash(hash) {
		return "", &revError{http.StatusBadRequest, field + " must be a full commit hash"}
	}
	sha, err := resolveCommit(ctx, hash, hash, field)
	if err != nil {
		return "", err
	}
	if sha != hash {
		return "", &revError{http.StatusBadRequest, fmt.Sprintf("%s %s is not a commit", field, hash)}
	}
	return sha, nil
}

// trialMerge works out the commit a merge of source into target would test,
// without moving either: the tree it makes is the one a merge, squash or
// fast-forward lands.
func trialMerge(ctx context.Context, source, target string) (string, *repoPipelineConflict, error) {
	source, err := pipelineCommit(ctx, source, "source")
	if err != nil {
		return "", nil, err
	}
	target, err = pipelineCommit(ctx, target, "target")
	if err != nil {
		return "", nil, err
	}
	switch {
	case gitExec(ctx, "merge-base", "--is-ancestor", source, target) == nil:
		// Nothing to merge: the target already has it.
		return target, nil, nil
	case gitExec(ctx, "merge-base", "--is-ancestor", target, source) == nil:
		// A fast-forward lands the source itself, and a merge or squash its
		// tree.
		return source, nil, nil
	}
	if out, err := gitOutput(ctx, "merge-base", target, source); err != nil || strings.TrimSpace(out) == "" {
		return "", &repoPipelineConflict{Message: "the source and target have no common history"}, nil
	}
	tree, conflicts, err := mergeTree(ctx, target, source)
	if err != nil {
		return "", nil, err
	}
	if len(conflicts) > 0 {
		return "", &repoPipelineConflict{Message: "the source conflicts with the target", Paths: conflicts}, nil
	}
	sha, err := trialMergeCommit(ctx, tree, source, target)
	if err != nil {
		return "", nil, err
	}
	return sha, nil, nil
}

// trialMergeCommit makes the merge commit of tree onto target and source.
// It is dated as the later of the two, so the same pair always makes the
// same commit.
func trialMergeCommit(ctx context.Context, tree, source, target string) (string, error) {
	out, err := gitOutput(ctx, "show", "-s", "--format=%ct", target, source)
	if err != nil {
		return "", fmt.Errorf("reading commit dates: %s", strings.TrimSpace(out))
	}
	var latest int64
	for _, field := range strings.Fields(out) {
		if t, err := strconv.ParseInt(field, 10, 64); err == nil && t > latest {
			latest = t
		}
	}
	date := strconv.FormatInt(latest, 10) + " +0000"
	env := append(commitIdentityEnv("", ""), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	message := fmt.Sprintf("Merge %s into %s", source[:12], target[:12])
	out, err = gitOutputEnv(ctx, env, noHooks("commit-tree", "--no-gpg-sign", tree, "-p", target, "-p", source, "-m", message)...)
	if err != nil {
		return "", fmt.Errorf("creating the trial merge: %s", strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

// pipelineRef returns the ref of the pipeline a request names.
func pipelineRef(r *http.Request) (string, error) {
	id := r.URL.Query().Get("id")
	if !pipelineIDPattern.MatchString(id) {
		return "", errors.New("id must be lowercase letters, digits and hyphens")
	}
	return pipelineRefPrefix + id, nil
}

// handleRepoPipelineBundle streams a bundle of what a pipeline tests.
func handleRepoPipelineBundle(w http.ResponseWriter, r *http.Request) {
	ref, err := pipelineRef(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()
	if gitExec(ctx, "rev-parse", "--verify", "--quiet", ref) != nil {
		writeErr(w, http.StatusNotFound, "nothing is pinned for this pipeline")
		return
	}
	writeBundle(ctx, w, []string{ref})
}

// handleRepoDeletePipelineRef deletes a pipeline's ref, if it has one.
func handleRepoDeletePipelineRef(w http.ResponseWriter, r *http.Request) {
	ref, err := pipelineRef(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()
	if out, err := gitOutput(ctx, noHooks("update-ref", "-d", ref)...); err != nil {
		writeErr(w, http.StatusInternalServerError, "deleting "+ref+": "+strings.TrimSpace(out))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ref": ref})
}
