package project

import (
	"errors"
	"time"
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
	ErrBusy          = errors.New("project is busy")
	ErrDefaultBranch = errors.New("sandboxes cannot own the default branch")
	// ErrPublishDefault refuses publishing the default branch from the
	// project workspace; it only moves through a merge.
	ErrPublishDefault = errors.New("the default branch cannot be published; merge into it through a merge request")
	ErrNoWorkspace    = errors.New("project has no workspace yet")
	ErrInvalidInput   = errors.New("invalid input")
)

// Project is a repo that AI agents work on in parallel sandboxes. Its
// canonical bare repo lives in a repo container that only devpad can reach.
type Project struct {
	ID            int64
	UserID        int64
	Name          string
	Description   string
	DefaultBranch string
	UpstreamURL   string
	SetupScript   string
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

// UpdateInput holds the editable project fields. The default branch and
// upstream are fixed once the repo exists.
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

// MergeInput merges Source into the project's default branch.
type MergeInput struct {
	Source string
	// Strategy is merge, squash or fast-forward; empty means merge.
	Strategy string
	Message  string
	// ExpectedSourceSHA refuses the merge if Source has moved past it.
	ExpectedSourceSHA string
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
