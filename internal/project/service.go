package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/encrypt"
	"github.com/devpad-org/devpad/internal/workspace"
)

// Service defines project and sandbox operations.
type Service interface {
	Create(ctx context.Context, userID int64, in CreateInput) (*Project, error)
	List(ctx context.Context, userID int64) ([]*Project, error)
	// Overviews summarizes every project's activity for the launcher.
	Overviews(ctx context.Context, userID int64) ([]*Overview, []*WorkspaceLink, error)
	Get(ctx context.Context, userID, projectID int64) (*Project, error)
	Update(ctx context.Context, userID, projectID int64, in UpdateInput) (*Project, error)
	Delete(ctx context.Context, userID, projectID int64) error
	Branches(ctx context.Context, userID, projectID int64) (*agent.RepoRefs, error)
	ListSandboxes(ctx context.Context, userID, projectID int64) ([]*workspace.Workspace, error)
	CreateSandbox(ctx context.Context, userID, projectID int64, in SandboxInput) (*workspace.Workspace, error)
	StartSandbox(ctx context.Context, userID, projectID, workspaceID int64) (*workspace.Workspace, error)
	PublishSandbox(ctx context.Context, userID, projectID, workspaceID int64) (*agent.RepoBranch, error)
	SyncSandboxBase(ctx context.Context, userID, projectID, workspaceID int64) (*agent.RepoBranch, error)
	Compare(ctx context.Context, userID, projectID int64, base, head string) (*agent.RepoComparison, error)
	Diff(ctx context.Context, userID, projectID int64, base, head string) (*agent.RepoDiff, error)
	FileAt(ctx context.Context, userID, projectID int64, rev, path string) ([]byte, error)
	Merge(ctx context.Context, userID, projectID int64, in MergeInput) (*agent.RepoMergeResult, error)
	Push(ctx context.Context, userID, projectID int64) (*agent.RepoBranch, error)
	Workspace(ctx context.Context, userID, projectID int64) (*workspace.Workspace, error)
	OpenWorkspace(ctx context.Context, userID, projectID int64) (ws *workspace.Workspace, created bool, err error)
	CheckoutWorkspaceBranch(ctx context.Context, userID, projectID int64, branch string) (*agent.RepoCheckout, error)
	PublishWorkspaceBranch(ctx context.Context, userID, projectID int64, branch string) (*agent.RepoBranch, error)
	SyncWorkspaceBase(ctx context.Context, userID, projectID int64) (*agent.RepoBranch, error)
	// FetchWorkspaceBranches refreshes the project workspace's devpad/<branch>
	// copies of canonical branches, for git fetch through the devpad remote.
	FetchWorkspaceBranches(ctx context.Context, userID, projectID int64, branches []string) error
	WorkspaceBranchStatus(ctx context.Context, userID, projectID int64, branch string) (*WorkspaceBranchStatus, error)
	// WorkspaceSetup returns the latest setup run in the project workspace,
	// or nil; RunWorkspaceSetup runs the setup script there again.
	WorkspaceSetup(ctx context.Context, userID, projectID int64) (*Setup, error)
	RunWorkspaceSetup(ctx context.Context, userID, projectID int64) (*Setup, error)
	// AwaitSetup waits for the setup run in a sandbox to finish and returns
	// it, or nil when none runs there.
	AwaitSetup(ctx context.Context, userID, projectID, workspaceID int64) (*Setup, error)
	// DiscoverConversion lists what a standalone workspace holds that could
	// become a project.
	DiscoverConversion(ctx context.Context, userID, workspaceID int64) (*agent.RepoDiscovery, error)
	ConvertWorkspace(ctx context.Context, userID, workspaceID int64, in ConvertInput) (*ConvertResult, error)
	// ConversionNotice returns what converting a workspace into the project
	// did, until dismissed; nil when there is nothing to show.
	ConversionNotice(ctx context.Context, userID, projectID int64) (*Conversion, error)
	DismissConversionNotice(ctx context.Context, userID, projectID int64) error
	// Reconcile finishes conversions devpad stopped part way through.
	Reconcile(ctx context.Context) error
	// SetPublishHook registers who is told when a branch is published from
	// the project workspace.
	SetPublishHook(h PublishHook)
}

// PublishHook is told each time a branch is published from the project
// workspace, with the commit it now points at, so an open merge request for
// the branch can follow it.
type PublishHook interface {
	BranchPublished(ctx context.Context, userID, projectID int64, branch, sha string)
}

// KeyRotator replaces a user's devpad SSH key, returning the new public key.
// Converting a workspace in place rotates the key the workspace held.
type KeyRotator interface {
	GenerateSSHKey(ctx context.Context, userID int64) (publicKey string, err error)
}

// Workspaces is the workspace service surface projects need to run sandboxes
// and the project workspace.
type Workspaces interface {
	workspace.AccessService
	workspace.SandboxService
	workspace.ProjectWorkspaceService
	workspace.ConversionService
	Delete(ctx context.Context, userID, workspaceID int64) error
	Start(ctx context.Context, userID, workspaceID int64) (*workspace.Workspace, error)
	StartCommand(ctx context.Context, userID, workspaceID int64, command, cwd string) (*agent.ManagedCommand, error)
	ReadCommandOutput(ctx context.Context, userID, workspaceID int64, commandID string, cursor int64, maxBytes, waitMS int) (*agent.CommandOutput, error)
	StopCommand(ctx context.Context, userID, workspaceID int64, commandID string) (*agent.ManagedCommand, error)
}

type service struct {
	repo       Repository
	container  container.Manager
	workspaces Workspaces
	users      auth.UserRepository
	cipher     *encrypt.Cipher
	keys       KeyRotator
	agentPort  int
	// convertTimeout bounds converting a workspace into a project.
	convertTimeout time.Duration
	// setupTimeout bounds a setup run; setupPoll paces reads of its output.
	setupTimeout time.Duration
	setupPoll    time.Duration

	// pending holds, per project, the branches of sandboxes being created or
	// started, so concurrent requests cannot overshoot MaxSandboxes while
	// Docker work is in flight.
	mu      sync.Mutex
	pending map[int64]map[string]bool
	// publishing holds, per project, the branches being published from the
	// project workspace, which no sandbox may claim meanwhile.
	publishing map[int64]map[string]bool
	// deleting holds the projects being deleted, which take no new
	// sandboxes, publishes or project workspace.
	deleting map[int64]bool
	// converting holds the projects being made from a workspace, which
	// cannot be deleted until the conversion settles.
	converting map[int64]bool
	// workspaceLocks serialize opening each project's workspace.
	workspaceLocks map[int64]*sync.Mutex
	// setupWatches holds, per workspace, the setup run being followed,
	// closed once it is no longer.
	setupWatches map[int64]chan struct{}
	publishHook  PublishHook
	// background tracks conversions being finished and setup runs being
	// followed in the background.
	background sync.WaitGroup
}

// NewService creates a new project Service.
func NewService(repo Repository, cm container.Manager, workspaces Workspaces, users auth.UserRepository, cipher *encrypt.Cipher, keys KeyRotator) Service {
	return &service{
		repo:       repo,
		container:  cm,
		workspaces: workspaces,
		users:      users,
		cipher:     cipher,
		keys:       keys,
		agentPort:  agent.DefaultPort,

		convertTimeout: workspaceOpenTimeout,
		setupTimeout:   defaultSetupTimeout,
		setupPoll:      defaultSetupPoll,
		pending:        make(map[int64]map[string]bool),

		publishing:     make(map[int64]map[string]bool),
		deleting:       make(map[int64]bool),
		converting:     make(map[int64]bool),
		workspaceLocks: make(map[int64]*sync.Mutex),
		setupWatches:   make(map[int64]chan struct{}),
	}
}

func (s *service) SetPublishHook(h PublishHook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishHook = h
}

var branchNamePattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// validBranchName is a conservative pre-check; the agent runs git's own
// check-ref-format before using any name.
// ValidBranchName reports whether name is a branch name devpad accepts.
func ValidBranchName(name string) bool {
	return validBranchName(name)
}

func validBranchName(name string) bool {
	return branchNamePattern.MatchString(name) &&
		!strings.HasPrefix(name, "-") && !strings.HasPrefix(name, "/") &&
		!strings.HasSuffix(name, "/") && !strings.HasSuffix(name, ".") && !strings.HasSuffix(name, ".lock") &&
		!strings.Contains(name, "..") && !strings.Contains(name, "//")
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}

func normalizeMaxSandboxes(n int) (int, error) {
	if n == 0 {
		return DefaultMaxSandboxes, nil
	}
	if n < 1 || n > MaxSandboxesLimit {
		return 0, invalid("maxSandboxes must be between 1 and %d", MaxSandboxesLimit)
	}
	return n, nil
}

func (s *service) Create(ctx context.Context, userID int64, in CreateInput) (*Project, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, invalid("name is required")
	}
	branch := strings.TrimSpace(in.DefaultBranch)
	if branch == "" {
		branch = DefaultBranch
	}
	if !validBranchName(branch) {
		return nil, invalid("defaultBranch is not a valid branch name")
	}
	upstream := strings.TrimSpace(in.UpstreamURL)
	if strings.HasPrefix(upstream, "-") {
		return nil, invalid("upstreamUrl must not start with '-'")
	}
	maxSandboxes, err := normalizeMaxSandboxes(in.MaxSandboxes)
	if err != nil {
		return nil, err
	}
	token, err := randomHex(32)
	if err != nil {
		return nil, fmt.Errorf("generating repo agent token: %w", err)
	}

	p := &Project{
		UserID:         userID,
		Name:           name,
		Description:    in.Description,
		DefaultBranch:  branch,
		UpstreamURL:    upstream,
		SetupScript:    in.SetupScript,
		MaxSandboxes:   maxSandboxes,
		Status:         StatusCreating,
		RepoAgentToken: token,
	}
	// Insert first so Docker resource names can use the stable project ID.
	if err := s.repo.Create(ctx, p); err != nil {
		return nil, fmt.Errorf("creating project: %w", err)
	}
	if err := s.provisionRepoContainer(ctx, p); err != nil {
		_ = s.repo.Delete(ctx, p.ID)
		return nil, err
	}

	// From here on the Docker resources exist, so failures are recorded on
	// the project instead of rolled back; the user can inspect and delete it.
	p.Status, p.Error = StatusReady, ""
	if err := s.initRepo(ctx, p, false); err != nil {
		log.Printf("project %d: repo init failed: %v", p.ID, err)
		p.Status, p.Error = StatusError, err.Error()
	}
	if err := s.repo.Update(ctx, p); err != nil {
		return nil, fmt.Errorf("updating project: %w", err)
	}
	return p, nil
}

// initRepo sets up the canonical repo, cloned from the upstream if there is
// one. With empty set it clones nothing and makes no commit, so a converted
// workspace's branches can be imported into it.
func (s *service) initRepo(ctx context.Context, p *Project, empty bool) error {
	if _, err := s.waitForRepoAgent(ctx, p); err != nil {
		return err
	}
	// The image's agent can predate this server's; repoAgent replaces it.
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return err
	}
	// Only the repo container gets the user's SSH key, and only when it has
	// to clone a private upstream. It never runs project code.
	if p.UpstreamURL != "" {
		user, err := s.users.GetByID(ctx, p.UserID)
		if err != nil {
			return fmt.Errorf("looking up user: %w", err)
		}
		if err := workspace.WriteSSHKeys(ctx, c, user, s.cipher); err != nil {
			return fmt.Errorf("injecting SSH keys into repo container: %w", err)
		}
	}
	if empty {
		_, err = c.RepoInitEmpty(ctx, p.DefaultBranch, p.UpstreamURL)
	} else {
		_, err = c.RepoInit(ctx, p.DefaultBranch, p.UpstreamURL)
	}
	if err != nil {
		return fmt.Errorf("initializing repo: %w", err)
	}
	return nil
}

func (s *service) List(ctx context.Context, userID int64) ([]*Project, error) {
	projects, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	return projects, nil
}

func (s *service) Get(ctx context.Context, userID, projectID int64) (*Project, error) {
	p, err := s.repo.GetByID(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("getting project: %w", err)
	}
	if p == nil {
		return nil, ErrNotFound
	}
	if p.UserID != userID {
		return nil, ErrForbidden
	}
	// A converted project left "creating" by a failed step is finished in
	// the background; the caller sees it ready on a later read.
	if p.Status == StatusCreating {
		s.healConversion(ctx, p)
	}
	return p, nil
}

func (s *service) getReady(ctx context.Context, userID, projectID int64) (*Project, error) {
	p, err := s.Get(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if p.Status != StatusReady {
		return nil, ErrNotReady
	}
	return p, nil
}

func (s *service) Update(ctx context.Context, userID, projectID int64, in UpdateInput) (*Project, error) {
	p, err := s.Get(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, invalid("name is required")
	}
	maxSandboxes, err := normalizeMaxSandboxes(in.MaxSandboxes)
	if err != nil {
		return nil, err
	}
	p.Name = name
	p.Description = in.Description
	p.SetupScript = in.SetupScript
	p.MaxSandboxes = maxSandboxes
	if err := s.repo.Update(ctx, p); err != nil {
		return nil, fmt.Errorf("updating project: %w", err)
	}
	return p, nil
}

// Delete removes every sandbox and the project workspace through the
// workspace lifecycle, then the repo container and the project. Published
// branches are lost with the repo. Sandbox creations and publishes in flight
// make it fail; once it starts, new ones are refused, and it waits for an
// open of the project workspace to finish, so the list of workspaces it
// removes is final before the repo goes.
func (s *service) Delete(ctx context.Context, userID, projectID int64) error {
	p, err := s.Get(ctx, userID, projectID)
	if err != nil {
		return err
	}
	if err := s.beginDelete(p.ID); err != nil {
		return err
	}
	defer s.endDelete(p.ID)
	unlock := s.lockWorkspace(p.ID)
	defer unlock()
	// Re-read under the lock: an open or conversion that held it may have
	// changed the project, such as recording its repo container.
	if p, err = s.Get(ctx, userID, projectID); err != nil {
		return err
	}

	sandboxes, err := s.workspaces.ListByProject(ctx, userID, p.ID)
	if err != nil {
		return err
	}
	for _, ws := range sandboxes {
		if err := s.workspaces.Delete(ctx, userID, ws.ID); err != nil {
			return fmt.Errorf("deleting sandbox %d: %w", ws.ID, err)
		}
	}
	if err := s.removeRepoContainer(ctx, p); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, p.ID); err != nil {
		return fmt.Errorf("deleting project: %w", err)
	}
	return nil
}

// beginDelete marks a project as being deleted, unless sandboxes are being
// created or branches published, which would add to what Delete removes.
func (s *service) beginDelete(projectID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleting[projectID] {
		return ErrDeleting
	}
	if s.converting[projectID] {
		return fmt.Errorf("%w: wait for the workspace conversion to finish", ErrBusy)
	}
	if len(s.pending[projectID]) > 0 || len(s.publishing[projectID]) > 0 {
		return fmt.Errorf("%w: wait for sandboxes being created and branches being published", ErrBusy)
	}
	s.deleting[projectID] = true
	return nil
}

func (s *service) endDelete(projectID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.deleting, projectID)
}

func (s *service) Branches(ctx context.Context, userID, projectID int64) (*agent.RepoRefs, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	return c.RepoRefs(ctx)
}

func (s *service) ListSandboxes(ctx context.Context, userID, projectID int64) ([]*workspace.Workspace, error) {
	if _, err := s.Get(ctx, userID, projectID); err != nil {
		return nil, err
	}
	all, err := s.workspaces.ListByProject(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	sandboxes := make([]*workspace.Workspace, 0, len(all))
	for _, ws := range all {
		if ws.IsSandbox() {
			sandboxes = append(sandboxes, ws)
		}
	}
	return sandboxes, nil
}

// CreateSandbox starts a sandbox that owns one branch of the project. A
// branch that already exists in the canonical repo is resumed; otherwise it
// starts from the default branch. The setup script, if any, starts once the
// repo is seeded; AwaitSetup waits for it to finish.
func (s *service) CreateSandbox(ctx context.Context, userID, projectID int64, in SandboxInput) (*workspace.Workspace, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}

	kind := workspace.Kind(in.Kind)
	if kind == "" {
		kind = workspace.KindTask
	}
	if kind == workspace.KindInteractive || !kind.Valid() {
		return nil, invalid("kind must be %q or %q", workspace.KindTask, workspace.KindReview)
	}
	branch := strings.TrimSpace(in.Branch)
	if branch == "" {
		suffix, err := randomHex(3)
		if err != nil {
			return nil, err
		}
		branch = "sandbox-" + suffix
	}
	if !validBranchName(branch) {
		return nil, invalid("branch is not a valid branch name")
	}
	if branch == p.DefaultBranch {
		return nil, ErrDefaultBranch
	}

	if err := s.reserveSandbox(ctx, p, branch, false); err != nil {
		return nil, err
	}
	defer s.releaseSandbox(p.ID, branch)

	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	refs, err := c.RepoRefs(ctx)
	if err != nil {
		return nil, err
	}
	exists := make(map[string]bool, len(refs.Branches))
	for _, ref := range refs.Branches {
		exists[ref.Name] = true
	}
	base := p.DefaultBranch
	switch {
	case exists[branch]:
		base = branch
	case in.Base != "":
		if !validBranchName(in.Base) || !exists[in.Base] {
			return nil, invalid("base branch %q does not exist", in.Base)
		}
		base = in.Base
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = p.Name + ": " + branch
	}
	ws, err := s.workspaces.CreateSandbox(ctx, userID, workspace.SandboxSpec{
		ProjectID: p.ID,
		Kind:      kind,
		Branch:    branch,
		Name:      name,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, ErrBranchInUse
		}
		return nil, err
	}

	if err := s.seedSandbox(ctx, c, userID, ws.ID, base); err != nil {
		if delErr := s.workspaces.Delete(ctx, userID, ws.ID); delErr != nil {
			log.Printf("project %d: removing unseeded sandbox %d: %v", p.ID, ws.ID, delErr)
		}
		return nil, fmt.Errorf("seeding sandbox: %w", err)
	}

	if _, err := s.startSetup(ctx, p, ws); err != nil {
		log.Printf("project %d: starting setup script in sandbox %d: %v", p.ID, ws.ID, err)
	}
	return ws, nil
}

// seedSandbox streams a bundle of base from the repo container straight into
// the sandbox; devpad never buffers it.
func (s *service) seedSandbox(ctx context.Context, repo *agent.Client, userID, workspaceID int64, base string) error {
	bundle, err := repo.RepoBundle(ctx, base)
	if err != nil {
		return err
	}
	defer bundle.Close()
	_, err = s.workspaces.SeedSandboxRepo(ctx, userID, workspaceID, base, bundle)
	return err
}

// reserveSandbox checks the sandbox limit and branch ownership for a sandbox
// about to be created, or restarted when starting is set. The limit counts
// sandboxes that are not stopped plus creations and starts still in flight;
// stopped sandboxes keep their branch but use no CPU or memory.
func (s *service) reserveSandbox(ctx context.Context, p *Project, branch string, starting bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := s.workspaces.ListByProject(ctx, p.UserID, p.ID)
	if err != nil {
		return err
	}
	if s.deleting[p.ID] {
		return ErrDeleting
	}
	pending := s.pending[p.ID]
	if pending[branch] {
		return ErrBranchInUse
	}
	if s.publishing[p.ID][branch] {
		return ErrBranchPublishing
	}
	active := 0
	for _, ws := range existing {
		// The project workspace owns no branch and has no limit.
		if !ws.IsSandbox() {
			continue
		}
		if !starting && ws.Branch == branch {
			return ErrBranchInUse
		}
		// A sandbox whose creation or start is in flight is counted once,
		// through pending, even after its row appears.
		if ws.Status != workspace.StatusStopped && !pending[ws.Branch] {
			active++
		}
	}
	if active+len(pending) >= p.MaxSandboxes {
		return ErrSandboxLimit
	}
	if pending == nil {
		pending = make(map[string]bool)
		s.pending[p.ID] = pending
	}
	pending[branch] = true
	return nil
}

func (s *service) releaseSandbox(projectID int64, branch string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending[projectID], branch)
	if len(s.pending[projectID]) == 0 {
		delete(s.pending, projectID)
	}
}

// StartSandbox restarts a stopped sandbox, counting it against the project's
// sandbox limit.
func (s *service) StartSandbox(ctx context.Context, userID, projectID, workspaceID int64) (*workspace.Workspace, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	ws, err := s.getSandbox(ctx, userID, p, workspaceID)
	if err != nil {
		return nil, err
	}
	if ws.Status != workspace.StatusStopped {
		return ws, nil
	}
	if err := s.reserveSandbox(ctx, p, ws.Branch, true); err != nil {
		return nil, err
	}
	defer s.releaseSandbox(p.ID, ws.Branch)
	return s.workspaces.Start(ctx, userID, ws.ID)
}

// getSandbox loads a workspace and checks that it is a sandbox of p.
func (s *service) getSandbox(ctx context.Context, userID int64, p *Project, workspaceID int64) (*workspace.Workspace, error) {
	ws, err := s.workspaces.Get(ctx, userID, workspaceID)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrForbidden) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if ws.ProjectID != p.ID || !ws.IsSandbox() {
		return nil, ErrNotFound
	}
	return ws, nil
}

// SyncSandboxBase refreshes a sandbox's devpad/<default branch> from the
// canonical repo, so its agent can merge in work that has landed since.
func (s *service) SyncSandboxBase(ctx context.Context, userID, projectID, workspaceID int64) (*agent.RepoBranch, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	ws, err := s.getSandbox(ctx, userID, p, workspaceID)
	if err != nil {
		return nil, err
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	bundle, err := c.RepoBundle(ctx, p.DefaultBranch)
	if err != nil {
		return nil, err
	}
	defer bundle.Close()
	return s.workspaces.FetchSandboxBase(ctx, userID, ws.ID, p.DefaultBranch, bundle)
}

// PublishSandbox copies the sandbox's branch into the canonical repo. Devpad
// pulls a bundle of the branch it recorded for the sandbox and imports only
// that branch, so a sandbox cannot write any other ref.
func (s *service) PublishSandbox(ctx context.Context, userID, projectID, workspaceID int64) (*agent.RepoBranch, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	ws, err := s.getSandbox(ctx, userID, p, workspaceID)
	if err != nil {
		return nil, err
	}
	if ws.Kind != workspace.KindTask {
		return nil, invalid("only task sandboxes publish their branch")
	}
	if ws.Branch == "" || ws.Branch == p.DefaultBranch {
		return nil, ErrDefaultBranch
	}

	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	branch, bundle, err := s.workspaces.BundleSandboxBranch(ctx, userID, ws.ID)
	if err != nil {
		return nil, err
	}
	defer bundle.Close()
	if branch != ws.Branch {
		return nil, fmt.Errorf("sandbox branch changed from %q to %q", ws.Branch, branch)
	}
	return c.RepoImport(ctx, ws.Branch, bundle)
}

// Compare reports how far head has diverged from base in the canonical repo.
// Each is a branch name or a full commit hash; base defaults to the default
// branch.
func (s *service) Compare(ctx context.Context, userID, projectID int64, base, head string) (*agent.RepoComparison, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if base == "" {
		base = p.DefaultBranch
	}
	if !validRev(base) || !validRev(head) {
		return nil, invalid("base and head must be branch names or commit hashes")
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	return c.RepoCompare(ctx, base, head)
}

// Diff lists the files head changed since it diverged from base, the default
// branch unless given. Both are branch names or full commit hashes.
func (s *service) Diff(ctx context.Context, userID, projectID int64, base, head string) (*agent.RepoDiff, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if base == "" {
		base = p.DefaultBranch
	}
	if !validRev(base) || !validRev(head) {
		return nil, invalid("base and head must be branch names or commit hashes")
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	return c.RepoDiff(ctx, base, head)
}

// FileAt returns a file's contents at a branch or commit of the canonical repo.
func (s *service) FileAt(ctx context.Context, userID, projectID int64, rev, path string) ([]byte, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if !validRev(rev) || path == "" {
		return nil, invalid("rev and path are required")
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	return c.RepoFile(ctx, rev, path)
}

// Merge merges a branch into the default branch, committing as the user.
func (s *service) Merge(ctx context.Context, userID, projectID int64, in MergeInput) (*agent.RepoMergeResult, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if !validBranchName(in.Source) || in.Source == p.DefaultBranch {
		return nil, invalid("source must be a branch other than the default branch")
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, fmt.Errorf("looking up user: %w", err)
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	return c.RepoMerge(ctx, agent.RepoMergeRequest{
		Source:            in.Source,
		Target:            p.DefaultBranch,
		Strategy:          in.Strategy,
		Message:           in.Message,
		ExpectedSourceSHA: in.ExpectedSourceSHA,
		Name:              user.Username,
		Email:             user.Email,
	})
}

// Push pushes the default branch to the project's upstream. It never forces.
func (s *service) Push(ctx context.Context, userID, projectID int64) (*agent.RepoBranch, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if p.UpstreamURL == "" {
		return nil, invalid("the project has no upstream")
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	// The key is written on every push, so a rotated key takes effect in
	// every project's repo container.
	user, err := s.users.GetByID(ctx, p.UserID)
	if err != nil {
		return nil, fmt.Errorf("looking up user: %w", err)
	}
	if err := workspace.WriteSSHKeys(ctx, c, user, s.cipher); err != nil {
		return nil, fmt.Errorf("injecting SSH keys into repo container: %w", err)
	}
	return c.RepoPush(ctx, p.DefaultBranch)
}

var hashPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// validRev accepts a branch name or a full commit hash.
func validRev(rev string) bool {
	return hashPattern.MatchString(rev) || validBranchName(rev)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
