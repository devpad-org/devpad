package project

import (
	"errors"
	"slices"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
)

// Status represents the setup state of a project's canonical repo.
type Status string

const (
	StatusCreating Status = "creating"
	StatusReady    Status = "ready"
	StatusError    Status = "error"
)

const (
	DefaultBranch       = "main"
	DefaultMaxSandboxes = 3
	MaxSandboxesLimit   = 20
	// MaxProtectedBranches bounds the branches a project protects besides
	// its default branch.
	MaxProtectedBranches = 20
)

var (
	ErrNotFound     = errors.New("project not found")
	ErrForbidden    = errors.New("access denied")
	ErrNotReady     = errors.New("project repo is not ready")
	ErrSandboxLimit = errors.New("project sandbox limit reached")
	ErrBranchInUse  = errors.New("branch already belongs to a sandbox")
	// ErrBranchPublishing refuses claiming a branch while it is being
	// published from the project workspace. It is temporary, so callers may
	// retry.
	ErrBranchPublishing = errors.New("branch is being published from the project workspace")
	// ErrDeleting refuses new sandboxes, publishes and the project workspace
	// while the project is being deleted.
	ErrDeleting = errors.New("project is being deleted")
	// ErrBusy refuses deleting a project while sandboxes are being created
	// or branches published.
	ErrBusy            = errors.New("project is busy")
	ErrProtectedBranch = errors.New("sandboxes cannot own a protected branch")
	// ErrPublishProtected refuses publishing a protected branch from the
	// project workspace; it only moves through a merge.
	ErrPublishProtected = errors.New("protected branches cannot be published; merge into them through a merge request")
	ErrNoWorkspace      = errors.New("project has no workspace yet")
	ErrInvalidInput     = errors.New("invalid input")
	ErrNoUpstream       = errors.New("the project has no upstream")
	// ErrDeleteProtected refuses deleting a protected branch; it must be
	// unprotected first, and the default branch replaced.
	ErrDeleteProtected = errors.New("protected branches cannot be deleted; unprotect it first")
	// ErrBranchHasMergeRequest refuses deleting a branch an open merge
	// request comes from.
	ErrBranchHasMergeRequest = errors.New("an open merge request comes from this branch; merge or close it first")
	// ErrBranchHasTask refuses deleting a branch a task that is not merged
	// or cancelled starts from or works on.
	ErrBranchHasTask = errors.New("a task still uses this branch")
)

// Project is a repo that AI agents work on in parallel sandboxes. Its
// canonical bare repo lives in a repo container that only devpad can reach.
type Project struct {
	ID            int64
	UserID        int64
	Name          string
	Description   string
	DefaultBranch string
	// ProtectedBranches are the branches besides the default that move only
	// through merge requests; merge requests can target any of them.
	ProtectedBranches []string
	// AutoMergeBranches are the protected branches a task's merge request
	// merges into on its own once a review agent approves it. Empty unless
	// the user picks some.
	AutoMergeBranches []string
	// PipelineBranches are the protected branches that run the project's
	// pipeline on each new commit, and on each merge request into them.
	// Empty unless the user picks some.
	PipelineBranches []string
	// PipelineConfig is the pipeline, in the format of .devpad/pipeline.yml,
	// for a commit that has no such file. Empty means none.
	PipelineConfig string
	UpstreamURL    string
	// UpstreamVersion counts the changes to UpstreamURL since the project
	// was made.
	UpstreamVersion int64
	SetupScript     string
	// Environment is the body of a Dockerfile devpad builds on its own
	// workspace image; EnvironmentImage is the last image that built from it,
	// empty when none has.
	Environment      string
	EnvironmentImage string
	MaxSandboxes     int
	AutoReview       bool
	Status           Status
	Error            string
	RepoContainerID  string
	RepoVolumeName   string
	RepoNetworkName  string
	RepoAgentToken   string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// IsProtected reports whether branch moves only through merge requests:
// the default branch or one of the project's protected branches.
func (p *Project) IsProtected(branch string) bool {
	if branch == p.DefaultBranch {
		return true
	}
	for _, b := range p.ProtectedBranches {
		if b == branch {
			return true
		}
	}
	return false
}

// AutoMerges reports whether a task's merge request into branch merges on
// its own once a review agent approves it. Only a protected branch can.
func (p *Project) AutoMerges(branch string) bool {
	return p.IsProtected(branch) && slices.Contains(p.AutoMergeBranches, branch)
}

// RunsPipelines reports whether branch runs the project's pipeline on each
// new commit and on each merge request into it. Only a protected branch
// can.
func (p *Project) RunsPipelines(branch string) bool {
	return p.IsProtected(branch) && slices.Contains(p.PipelineBranches, branch)
}

// PipelineFiles are where a commit keeps its pipeline, of which it may have
// one: internal/pipeline reads them, and an automatic merge never takes a
// change to them.
func PipelineFiles() []string {
	return []string{".devpad/pipeline.yml", ".devpad/pipeline.yaml"}
}

// IsPipelineFile reports whether path is one of PipelineFiles.
func IsPipelineFile(path string) bool {
	return slices.Contains(PipelineFiles(), path)
}

// ChangedPipelineFile names the pipeline file a change adds, changes,
// renames or deletes, as its diff lists them, or "" when it touches none:
// the one rule for whether a merge request proposes other jobs than its
// target's, which an automatic merge never takes and devpad runs for
// information.
func ChangedPipelineFile(files []agent.RepoDiffFile) string {
	for _, f := range files {
		for _, path := range []string{f.Path, f.OldPath} {
			if path != "" && IsPipelineFile(path) {
				return path
			}
		}
	}
	return ""
}

// Protected lists every protected branch, the default branch first.
func (p *Project) Protected() []string {
	return append([]string{p.DefaultBranch}, p.ProtectedBranches...)
}

// CreateInput holds the fields for a new project.
type CreateInput struct {
	Name          string
	Description   string
	DefaultBranch string
	UpstreamURL   string
	SetupScript   string
	Environment   string
	MaxSandboxes  int
	// AutoReview starts a review agent on each new merge request.
	AutoReview bool
}

// UpdateInput holds the editable project fields. The upstream changes
// through SetUpstream, and the branches through SetBranches.
type UpdateInput struct {
	Name         string
	Description  string
	SetupScript  string
	Environment  string
	MaxSandboxes int
	AutoReview   bool
}

// SandboxInput describes a sandbox to start. Empty fields get defaults.
type SandboxInput struct {
	Name   string
	Kind   string
	Branch string
	// Base is the existing branch a new Branch starts from, instead of the
	// default branch. It is ignored when Branch already exists.
	Base string
}

// BranchesInput sets a project's default branch and the other branches it
// protects. Every one must exist in the canonical repo.
type BranchesInput struct {
	DefaultBranch     string
	ProtectedBranches []string
	// AutoMergeBranches are the protected branches, of those set here, that
	// a task's merge request merges into once a review agent approves it.
	// Nil keeps the project's, less any branch no longer protected.
	AutoMergeBranches *[]string
	// PipelineBranches are the protected branches, of those set here, that
	// run the project's pipeline. Nil keeps the project's, less any branch
	// no longer protected.
	PipelineBranches *[]string
}

// MergeInput merges Source into Target, a protected branch.
type MergeInput struct {
	Source string
	// Target defaults to the default branch.
	Target string
	// Strategy is merge, squash or fast-forward; empty means merge.
	Strategy string
	Message  string
	// ExpectedSourceSHA refuses the merge if Source has moved past it.
	ExpectedSourceSHA string
	// ExpectedTargetSHA, when set, refuses the merge unless Target is still
	// at it when the repo merges, so nothing merged into Target meanwhile is
	// merged with the change unseen.
	ExpectedTargetSHA string
	// ExpectedTree, when set, refuses the merge unless it lands this tree:
	// the one the pipeline that let it through tested.
	ExpectedTree string
}

// WorkspaceBranchStatus says how a branch of the project workspace compares
// with the project's branch of the same name.
type WorkspaceBranchStatus struct {
	Branch string `json:"branch"`
	// Published is whether the project has the branch.
	Published bool `json:"published"`
	// SHA is the workspace's commit, CanonicalSHA the project's.
	SHA          string `json:"sha"`
	CanonicalSHA string `json:"canonicalSha,omitempty"`
	// Ahead counts the workspace's commits the project lacks, Behind the
	// project's commits the workspace lacks.
	Ahead  int `json:"ahead"`
	Behind int `json:"behind"`
	// SandboxOwned is set when an agent's sandbox owns the branch, so the
	// workspace cannot publish to it.
	SandboxOwned bool `json:"sandboxOwned"`
}
