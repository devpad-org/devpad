package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Upstream endpoints run in a project's canonical bare repo. Devpad fetches
// the upstream's branches into their own namespace, refs/upstream/heads/,
// never straight into the project's branches; it then fast-forwards only the
// protected branches it names, and only when the project's copy is strictly
// behind. Nothing here creates, deletes or forces a project branch.

const (
	upstreamRefPrefix = "refs/upstream/heads/"
	// upstreamFetchedAtKey records when the upstream was last fetched, in
	// the canonical repo's config, so it outlives devpad restarts.
	upstreamFetchedAtKey = "devpad.upstreamFetchedAt"
)

// upstreamBranch compares a protected branch with the upstream's copy.
// Ahead counts the project's commits the upstream lacks; Behind the reverse.
type upstreamBranch struct {
	Branch        string `json:"branch"`
	SHA           string `json:"sha"`
	UpstreamSHA   string `json:"upstreamSha"`
	Ahead         int    `json:"ahead"`
	Behind        int    `json:"behind"`
	FastForwarded bool   `json:"fastForwarded,omitempty"`
}

type upstreamStatus struct {
	FetchedAt *time.Time       `json:"fetchedAt,omitempty"`
	Branches  []upstreamBranch `json:"branches"`
}

// protectedBranches reads and checks the protected branches devpad names.
func protectedBranches(ctx context.Context, r *http.Request) ([]string, error) {
	branches := r.URL.Query()["protected"]
	if len(branches) > maxBundleBranches {
		return nil, fmt.Errorf("name at most %d protected branches", maxBundleBranches)
	}
	seen := make(map[string]bool, len(branches))
	unique := make([]string, 0, len(branches))
	for _, branch := range branches {
		if err := validateBranchName(ctx, branch, "protected"); err != nil {
			return nil, err
		}
		if !seen[branch] {
			seen[branch] = true
			unique = append(unique, branch)
		}
	}
	return unique, nil
}

// handleRepoUpstreamFetch fetches the upstream into refs/upstream/heads/,
// pruning branches the upstream deleted, then fast-forwards each named
// protected branch that is strictly behind its upstream copy.
func handleRepoUpstreamFetch(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	protected, err := protectedBranches(ctx, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if gitExec(ctx, "remote", "get-url", "origin") != nil {
		writeErr(w, http.StatusBadRequest, "the project has no upstream")
		return
	}
	if out, err := gitOutput(ctx, "fetch", "--prune", "--no-tags", "--no-write-fetch-head", "origin",
		"+refs/heads/*:"+upstreamRefPrefix+"*"); err != nil {
		msg := strings.TrimSpace(out)
		if isSSHHostKeyVerificationFailure(msg) {
			msg = "the upstream's SSH host key is not one devpad trusts: " + msg
		}
		writeErr(w, http.StatusBadGateway, "fetching from upstream: "+msg)
		return
	}
	if out, err := gitOutput(ctx, "config", upstreamFetchedAtKey, time.Now().UTC().Format(time.RFC3339)); err != nil {
		writeErr(w, http.StatusInternalServerError, "recording the fetch: "+strings.TrimSpace(out))
		return
	}

	forwarded := make(map[string]bool)
	for _, branch := range protected {
		sha, upstream := branchSHA(ctx, "refs/heads/"+branch), branchSHA(ctx, upstreamRefPrefix+branch)
		if sha == "" || upstream == "" || sha == upstream || gitExec(ctx, "merge-base", "--is-ancestor", sha, upstream) != nil {
			continue
		}
		// The old value makes update-ref fail if a merge moved the branch
		// since; it is then reported as it now is.
		if gitExec(ctx, "update-ref", "refs/heads/"+branch, upstream, sha) == nil {
			forwarded[branch] = true
		}
	}

	status, err := readUpstreamStatus(ctx, protected)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range status.Branches {
		status.Branches[i].FastForwarded = forwarded[status.Branches[i].Branch]
	}
	writeJSON(w, http.StatusOK, status)
}

// handleRepoUpstreamStatus compares the named protected branches with the
// upstream as last fetched, without fetching.
func handleRepoUpstreamStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	protected, err := protectedBranches(ctx, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	status, err := readUpstreamStatus(ctx, protected)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleRepoUpstreamRefs lists the upstream's branches as last fetched.
func handleRepoUpstreamRefs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	out, err := gitOutput(ctx, "for-each-ref", "--format=%(refname:strip=3) %(objectname)", upstreamRefPrefix)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "listing upstream branches: "+strings.TrimSpace(out))
		return
	}
	refs := &repoRefs{Branches: []repoRef{}}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, sha, ok := strings.Cut(line, " ")
		if ok {
			refs.Branches = append(refs.Branches, repoRef{Name: name, SHA: sha})
		}
	}
	if head, err := gitOutput(ctx, "symbolic-ref", "--short", "HEAD"); err == nil {
		refs.DefaultBranch = strings.TrimSpace(head)
	}
	writeJSON(w, http.StatusOK, refs)
}

func readUpstreamStatus(ctx context.Context, protected []string) (*upstreamStatus, error) {
	status := &upstreamStatus{Branches: make([]upstreamBranch, 0, len(protected))}
	if out, err := gitOutput(ctx, "config", "--get", upstreamFetchedAtKey); err == nil {
		if at, err := time.Parse(time.RFC3339, strings.TrimSpace(out)); err == nil {
			status.FetchedAt = &at
		}
	}
	for _, branch := range protected {
		b := upstreamBranch{
			Branch:      branch,
			SHA:         branchSHA(ctx, "refs/heads/"+branch),
			UpstreamSHA: branchSHA(ctx, upstreamRefPrefix+branch),
		}
		if b.SHA != "" && b.UpstreamSHA != "" && b.SHA != b.UpstreamSHA {
			out, err := gitOutput(ctx, "rev-list", "--left-right", "--count", b.SHA+"..."+b.UpstreamSHA)
			if err != nil {
				return nil, fmt.Errorf("comparing %s with the upstream: %s", branch, strings.TrimSpace(out))
			}
			if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d %d", &b.Ahead, &b.Behind); err != nil {
				return nil, fmt.Errorf("parsing rev-list output: %w", err)
			}
		}
		status.Branches = append(status.Branches, b)
	}
	return status, nil
}

// branchSHA returns the commit ref points at, or "" if there is none.
func branchSHA(ctx context.Context, ref string) string {
	out, err := gitOutput(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
