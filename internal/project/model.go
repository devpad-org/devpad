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
	ErrNotFound      = errors.New("project not found")
	ErrForbidden     = errors.New("access denied")
	ErrNotReady      = errors.New("project repo is not ready")
	ErrSandboxLimit  = errors.New("project sandbox limit reached")
	ErrBranchInUse   = errors.New("branch already belongs to a sandbox")
	ErrDefaultBranch = errors.New("sandboxes cannot own the default branch")
	ErrInvalidInput  = errors.New("invalid input")
)

// Project is a repo that AI agents work on in parallel sandboxes. Its
// canonical bare repo lives in a repo container that only devpad can reach.
type Project struct {
	ID              int64
	UserID          int64
	Name            string
	Description     string
	DefaultBranch   string
	UpstreamURL     string
	SetupScript     string
	MaxSandboxes    int
	Status          Status
	Error           string
	RepoContainerID string
	RepoVolumeName  string
	RepoNetworkName string
	RepoAgentToken  string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CreateInput holds the fields for a new project.
type CreateInput struct {
	Name          string
	Description   string
	DefaultBranch string
	UpstreamURL   string
	SetupScript   string
	MaxSandboxes  int
}

// UpdateInput holds the editable project fields. The default branch and
// upstream are fixed once the repo exists.
type UpdateInput struct {
	Name         string
	Description  string
	SetupScript  string
	MaxSandboxes int
}

// SandboxInput describes a sandbox to start. Empty fields get defaults.
type SandboxInput struct {
	Name   string
	Kind   string
	Branch string
}
