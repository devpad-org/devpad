package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/workspace"
)

// Converting a standalone workspace makes a project from one of its repos,
// or from its files when it has none. Devpad pulls a bundle of every branch
// and tag from the workspace and imports it into a new, empty canonical
// repo, the one time a bundle may set the default branch.
//
// A repo at /workspace stays where it is, and the workspace becomes the
// project's workspace: uncommitted work, untracked files and everything else
// in the volume survive. A project workspace holds no credentials, and an AI
// may have read everything this one held, so converting in place:
//   - refuses while the workspace holds credentials the user has not agreed
//     to strip, and strips the ones they agreed to;
//   - removes the SSH keys devpad put there;
//   - restarts the container, ending any process that kept a credential;
//   - rotates the user's devpad SSH key, which the workspace held, and the
//     user is told to revoke the old key and what was stripped.
//
// A repo in a subdirectory cannot become the project workspace's repo in
// place, because devpad works on the repo at /workspace; its branches seed
// the project, the project gets a fresh workspace when first opened, and the
// old workspace is left as it was, credentials included.

// conversionCleanupTimeout bounds removing what a failed conversion made. It
// has its own deadline, so a conversion that ran out of time still cleans up.
const conversionCleanupTimeout = 5 * time.Minute

// conversionFinishTimeout bounds the steps after a workspace is attached:
// making the project ready, restarting the container and rotating the key.
// They get a deadline of their own, so a conversion that used most of its
// time still finishes them.
const conversionFinishTimeout = 10 * time.Minute

// ConversionState is how far a conversion got.
type ConversionState string

const (
	// ConversionImporting is a conversion whose workspace is not attached
	// yet; it can still be undone.
	ConversionImporting ConversionState = "importing"
	// ConversionAttached is a conversion whose workspace is the project's
	// workspace, with steps that may still be pending.
	ConversionAttached ConversionState = "attached"
	ConversionDone     ConversionState = "done"
)

// Conversion records a workspace's conversion into a project, so the steps
// after the workspace is attached survive a crash or a lost response, and so
// the user sees what was done, until they dismiss it. Stripped credentials
// are kept by kind and location, never value.
type Conversion struct {
	ProjectID   int64
	UserID      int64
	WorkspaceID int64
	InPlace     bool
	// InitSHA is the commit of a repo made from the workspace's files, to
	// undo if the conversion stops before attaching.
	InitSHA  string
	State    ConversionState
	Stripped []agent.CredentialItem
	// SSHKept lists SSH files left in place because they differ from what
	// devpad wrote.
	SSHKept         []string
	RestartPending  bool
	RestartError    string
	RotationPending bool
	KeyRotated      bool
	// PublicKey is the user's new devpad SSH key once rotated.
	PublicKey     string
	RotationError string
	// OldKeyFingerprint is the fingerprint of the devpad SSH key the
	// workspace held. The key is only rotated while it is still that one, so
	// a rotation whose record was lost does not run again.
	OldKeyFingerprint string
	// ProgressError is set when recording a step failed, so the step may
	// have run twice; the user is told.
	ProgressError string
	Dismissed     bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ConvertInput describes the project to make from a standalone workspace.
type ConvertInput struct {
	// RepoPath picks a repo found in the workspace, relative to /workspace.
	// Empty makes a repo from the workspace's files.
	RepoPath string
	// IncludeSensitive commits files that usually hold secrets, such as
	// .env, when making a repo from the workspace's files.
	IncludeSensitive bool
	// StripCredentials are the IDs of the credentials the user agreed to
	// strip from a workspace converted in place. It must name every one the
	// workspace holds.
	StripCredentials []string
	Name             string
	Description      string
	DefaultBranch    string
	UpstreamURL      string
	SetupScript      string
	MaxSandboxes     int
}

// ConvertResult is a converted workspace's new project.
type ConvertResult struct {
	Project *Project
	// InPlace is set when the workspace became the project's workspace;
	// otherwise its repo was in a subdirectory and it was left as it was.
	// The fields below are only set in place.
	InPlace bool
	// Workspace is the converted workspace, when it could be read back.
	Workspace *workspace.Workspace
	// SSH reports which of devpad's SSH files were removed.
	SSH *agent.SSHCleanup
	// Stripped lists the credentials removed with the user's consent.
	Stripped []agent.CredentialItem
	// RestartError is set when the container could not be restarted; the
	// user should restart it.
	RestartError string
	// KeyRotated is set when the user's devpad SSH key was replaced, with
	// PublicKey the new one; RotationError says why it could not be.
	KeyRotated    bool
	PublicKey     string
	RotationError string
	// ProgressError is set when devpad could not record a step, which may
	// then run again.
	ProgressError string
}

func (s *service) DiscoverConversion(ctx context.Context, userID, workspaceID int64) (*agent.RepoDiscovery, error) {
	return s.workspaces.DiscoverRepos(ctx, userID, workspaceID)
}

// ConvertWorkspace makes a project from a running standalone workspace. Any
// failure before the workspace is attached removes the half-made project
// and its repo container, and a repo made from the workspace's files, so
// the workspace is left as it was, apart from credentials already stripped
// with the user's consent. Once the workspace is attached, the conversion
// stands, and the steps after it always run: a failed restart or key
// rotation is recorded and reported, not undone, and one cut short by a
// crash is finished when devpad starts.
func (s *service) ConvertWorkspace(ctx context.Context, userID, workspaceID int64, in ConvertInput) (*ConvertResult, error) {
	// Like opening a project workspace, conversion outlives the request, so
	// a client that goes away cannot leave a half-made project behind.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.convertTimeout)
	defer cancel()

	p, repoPath, err := s.convertInput(userID, in)
	if err != nil {
		return nil, err
	}
	inPlace := repoPath == "" || repoPath == "."

	release, err := s.workspaces.BeginConversion(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer release()

	var consented []agent.CredentialItem
	if inPlace {
		found, err := s.workspaces.DiscoverRepos(ctx, userID, workspaceID)
		if err != nil {
			return nil, err
		}
		// Devpad keeps its copies of project branches under the "devpad"
		// remote, so a repo staying in place must not have one of its own.
		for _, repo := range found.Repos {
			if repoPath == "." && repo.Path == "." && repo.HasDevpadRemote {
				return nil, invalid("the repo has a remote named %q, which devpad needs; rename it first", "devpad")
			}
		}
		var unconsented []agent.CredentialItem
		for _, item := range found.Credentials {
			if slices.Contains(in.StripCredentials, item.ID) {
				consented = append(consented, item)
			} else {
				unconsented = append(unconsented, item)
			}
		}
		if len(unconsented) > 0 {
			return nil, &workspace.CredentialsError{Items: unconsented}
		}
	}

	// Cleanup gets its own deadline, so it runs even if ctx has expired.
	cleanupCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.WithoutCancel(ctx), conversionCleanupTimeout)
	}

	conv := &Conversion{UserID: userID, WorkspaceID: workspaceID, InPlace: inPlace, State: ConversionImporting}
	if repoPath == "" {
		made, err := s.workspaces.InitRepoFromContents(ctx, userID, workspaceID, p.DefaultBranch, in.IncludeSensitive)
		if err != nil {
			return nil, fmt.Errorf("making a repo from the workspace's files: %w", err)
		}
		conv.InitSHA = made.SHA
		repoPath = "."
	}

	// The project stays "creating" until the workspace is attached, so
	// nothing else can open its workspace or start a sandbox meanwhile, and
	// it is marked as converting as it is created, so Delete refuses it.
	if err := s.createConverting(ctx, p, conv); err != nil {
		cctx, cancel := cleanupCtx()
		defer cancel()
		s.undoInit(cctx, conv)
		return nil, fmt.Errorf("creating project: %w", err)
	}
	defer s.endConverting(p.ID)
	unlock := s.lockWorkspace(p.ID)
	defer unlock()

	// A failed conversion removes the repo made from the workspace's files
	// first, while this conversion still holds the workspace. The project
	// and its record go only once that is settled; otherwise they are kept,
	// still "creating", so the undo is retried when devpad next looks.
	fail := func(err error) (*ConvertResult, error) {
		cctx, cancel := cleanupCtx()
		defer cancel()
		if s.undoInit(cctx, conv) {
			s.discardProject(cctx, p)
		} else {
			s.keepForUndo(cctx, p)
		}
		return nil, err
	}
	if err := s.provisionRepoContainer(ctx, p); err != nil {
		return fail(err)
	}
	// Record the repo container at once, so a project whose cleanup fails
	// can still be deleted with it.
	if err := s.repo.Update(ctx, p); err != nil {
		return fail(fmt.Errorf("recording repo container: %w", err))
	}
	if err := s.initRepo(ctx, p, true); err != nil {
		return fail(err)
	}
	if err := s.importWorkspaceRepo(ctx, p, userID, workspaceID, repoPath); err != nil {
		return fail(err)
	}

	if !inPlace {
		// Ready and done are written together: a ready project whose record
		// still said "importing" would look undoable after a crash. Nothing
		// was taken from the workspace, so there is nothing to show.
		p.Status, p.Error = StatusReady, ""
		conv.State, conv.Dismissed = ConversionDone, true
		if err := s.repo.UpdateWithConversion(ctx, p, conv); err != nil {
			p.Status = StatusCreating
			return fail(fmt.Errorf("updating project: %w", err))
		}
		return &ConvertResult{Project: p}, nil
	}

	// Record what follows attaching before it happens: once attached, the
	// conversion stands, and these steps must run even if devpad stops. The
	// key the workspace held is fingerprinted, so a rotation whose record
	// is lost is not repeated over a key the user already added upstream.
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fail(fmt.Errorf("looking up user: %w", err))
	}
	if user != nil {
		conv.OldKeyFingerprint = sshKeyFingerprint(user.SSHPublicKey)
	}
	conv.Stripped, conv.RestartPending, conv.RotationPending = consented, true, true
	if err := s.repo.UpdateConversion(ctx, conv); err != nil {
		return fail(err)
	}
	attached, err := s.workspaces.AttachToProject(ctx, userID, workspaceID, p.ID, in.StripCredentials)
	if err != nil {
		return fail(err)
	}

	// The conversion stands from here, on a context of its own.
	fctx, fcancel := context.WithTimeout(context.WithoutCancel(ctx), conversionFinishTimeout)
	defer fcancel()
	conv.State = ConversionAttached
	if attached.Stripped != nil {
		conv.Stripped = attached.Stripped
	}
	if attached.SSH != nil {
		conv.SSHKept = attached.SSH.Kept
	}
	s.saveConversion(fctx, conv)
	s.finishAttached(fctx, p, conv)

	result := &ConvertResult{
		Project:       p,
		InPlace:       true,
		SSH:           attached.SSH,
		Stripped:      conv.Stripped,
		RestartError:  conv.RestartError,
		KeyRotated:    conv.KeyRotated,
		PublicKey:     conv.PublicKey,
		RotationError: conv.RotationError,
		ProgressError: conv.ProgressError,
	}
	if ws, err := s.workspaces.Get(fctx, userID, workspaceID); err == nil {
		result.Workspace = ws
	} else {
		log.Printf("workspace %d: reading back after conversion: %v", workspaceID, err)
	}
	return result, nil
}

// finishAttached completes a conversion whose workspace is attached: it
// makes the project ready, restarts the container and rotates the key. Each
// step is recorded as done once it ran, so none runs twice; failures are
// recorded for the user, not returned. A project that could not be made
// ready keeps the conversion unfinished, to be retried.
func (s *service) finishAttached(ctx context.Context, p *Project, conv *Conversion) {
	ready := p.Status == StatusReady
	if !ready {
		// Written with the record, so a ready project's record always says
		// its workspace was attached.
		p.Status, p.Error = StatusReady, ""
		conv.State = ConversionAttached
		if err := s.repo.UpdateWithConversion(ctx, p, conv); err != nil {
			log.Printf("project %d: marking ready after conversion: %v", p.ID, err)
			p.Status = StatusCreating
		} else {
			ready = true
		}
	}
	if conv.RestartPending {
		conv.RestartError = ""
		if err := s.workspaces.RestartConverted(ctx, conv.UserID, conv.WorkspaceID); err != nil {
			log.Printf("workspace %d: restarting after conversion: %v", conv.WorkspaceID, err)
			conv.RestartError = err.Error()
		}
		conv.RestartPending = false
		s.saveConversion(ctx, conv)
	}
	if conv.RotationPending {
		s.rotateKey(ctx, conv)
		conv.RotationPending = false
		s.saveConversion(ctx, conv)
	}
	if ready {
		conv.State = ConversionDone
		s.saveConversion(ctx, conv)
	}
}

// saveConversion records a conversion's progress. The steps it records have
// run either way, so a failed write is not returned; it is logged and kept
// on the conversion, so the next write that succeeds, and the response,
// tell the user a step may run again.
func (s *service) saveConversion(ctx context.Context, conv *Conversion) {
	if err := s.repo.UpdateConversion(ctx, conv); err != nil {
		log.Printf("project %d: recording conversion: %v", conv.ProjectID, err)
		conv.ProgressError = "devpad could not record a step of the conversion, so it may run again: " + err.Error()
	}
}

// rotateKey replaces the user's devpad SSH key, which the converted
// workspace held while an AI could read it. It runs once the workspace is
// attached, so a failure leaves the old key working and the conversion
// standing; the user is told to rotate it from Settings.
func (s *service) rotateKey(ctx context.Context, conv *Conversion) {
	conv.KeyRotated, conv.PublicKey, conv.RotationError = false, "", ""
	user, err := s.users.GetByID(ctx, conv.UserID)
	if err != nil {
		conv.RotationError = "looking up your SSH key: " + err.Error()
		return
	}
	if user == nil || user.SSHPrivateKey == "" {
		return // no key was ever put in the workspace
	}
	if conv.OldKeyFingerprint != "" && sshKeyFingerprint(user.SSHPublicKey) != conv.OldKeyFingerprint {
		// The key the workspace held is already gone: replaced by an earlier
		// run of this step whose record was lost, or by the user. Replacing
		// it again would void a key the user may have added upstream.
		conv.KeyRotated, conv.PublicKey = true, user.SSHPublicKey
		return
	}
	if s.keys == nil {
		conv.RotationError = "key rotation is not available"
		return
	}
	publicKey, err := s.keys.GenerateSSHKey(ctx, conv.UserID)
	if err != nil {
		log.Printf("user %d: rotating SSH key after a conversion: %v", conv.UserID, err)
		conv.RotationError = err.Error()
		return
	}
	conv.KeyRotated, conv.PublicKey = true, publicKey
}

// resumeConversion finishes a conversion that devpad stopped part way
// through. One whose workspace was attached finishes its pending steps. One
// whose workspace provably was not, from devpad's own records, is undone, as
// a failed conversion would have been, but only while its project is still
// "creating": a project that got further may be in use and is never removed.
// Anything devpad cannot tell is left alone and tried again later. The
// caller holds the project's converting mark and read p afresh.
func (s *service) resumeConversion(ctx context.Context, p *Project, conv *Conversion) {
	if conv.State == ConversionImporting {
		attached, known := s.conversionAttached(ctx, conv)
		switch {
		case p.Status != StatusCreating && !conv.InPlace:
			// Ready and done are written together, so this is a record from
			// before that, whose last write was lost; there is nothing to do.
			conv.State, conv.Dismissed = ConversionDone, true
			s.saveConversion(ctx, conv)
			return
		case !known:
			log.Printf("project %d: cannot tell whether workspace %d was attached; leaving the conversion to retry", p.ID, conv.WorkspaceID)
			return
		case attached:
			conv.State = ConversionAttached
			s.saveConversion(ctx, conv)
		case p.Status != StatusCreating:
			log.Printf("project %d is %s but workspace %d is not attached to it; leaving the conversion alone", p.ID, p.Status, conv.WorkspaceID)
			return
		default:
			s.undoConversion(ctx, p, conv)
			return
		}
	}
	if conv.State == ConversionAttached {
		s.finishAttached(ctx, p, conv)
	}
}

// undoConversion removes a conversion that stopped before its workspace was
// attached. The repo made from the workspace's files is removed first, under
// the workspace's conversion mark, so no new conversion can be using it; the
// project and its record are removed only once that is settled, so an undo
// that could not run is tried again.
func (s *service) undoConversion(ctx context.Context, p *Project, conv *Conversion) {
	if conv.InitSHA != "" {
		release, err := s.workspaces.BeginConversion(ctx, conv.UserID, conv.WorkspaceID)
		switch {
		case errors.Is(err, workspace.ErrNotStandalone):
			// Converted since, so the repo is in use: leave it.
			log.Printf("workspace %d was converted since; the repo made for project %d was left in place", conv.WorkspaceID, p.ID)
		case err != nil:
			log.Printf("project %d: cannot undo its conversion yet: %v", p.ID, err)
			return
		default:
			settled := s.undoInit(ctx, conv)
			release()
			if !settled {
				return
			}
		}
	}
	log.Printf("project %d: undoing a conversion that stopped before its workspace was attached", p.ID)
	s.discardProject(ctx, p)
}

// undoInit removes the repo a conversion made from the workspace's files.
// The caller holds the workspace's conversion mark. It reports whether that
// is settled: removed, never made, or left in place because it changed
// since devpad made it or the workspace is no longer standalone. False means
// it could not be tried, so the caller keeps what it needs to retry.
func (s *service) undoInit(ctx context.Context, conv *Conversion) bool {
	if conv.InitSHA == "" {
		return true
	}
	err := s.workspaces.UndoInitRepoFromContents(ctx, conv.UserID, conv.WorkspaceID, conv.InitSHA)
	var agentErr *agent.AgentError
	switch {
	case err == nil:
		return true
	case errors.As(err, &agentErr) && agentErr.StatusCode == http.StatusConflict:
		log.Printf("workspace %d: the repo made for a failed conversion changed since; it was left in place", conv.WorkspaceID)
		return true
	case errors.Is(err, workspace.ErrNotStandalone):
		log.Printf("workspace %d was converted since; the repo made for a failed conversion was left in place", conv.WorkspaceID)
		return true
	default:
		log.Printf("workspace %d: removing the repo made for a failed conversion: %v", conv.WorkspaceID, err)
		return false
	}
}

// keepForUndo removes the repo container of a failed conversion whose repo
// made from files could not be removed yet, and keeps the project,
// "creating", with its record, so the undo is retried by Reconcile or the
// next read of the project. The user can also delete it.
func (s *service) keepForUndo(ctx context.Context, p *Project) {
	log.Printf("project %d: kept until the repo made for its failed conversion can be removed", p.ID)
	if err := s.removeRepoContainer(ctx, p); err != nil {
		log.Printf("project %d: removing repo container after a failed conversion: %v", p.ID, err)
		return
	}
	p.RepoContainerID, p.RepoVolumeName, p.RepoNetworkName = "", "", ""
	if err := s.repo.Update(ctx, p); err != nil {
		log.Printf("project %d: recording its removed repo container: %v", p.ID, err)
	}
}

// conversionAttached reports from devpad's records whether a conversion's
// workspace became the project's workspace; known is false when that cannot
// be told, such as when the workspace is gone.
func (s *service) conversionAttached(ctx context.Context, conv *Conversion) (attached, known bool) {
	if !conv.InPlace {
		return false, true
	}
	ws, err := s.workspaces.Get(ctx, conv.UserID, conv.WorkspaceID)
	if err != nil {
		log.Printf("workspace %d: checking a stopped conversion: %v", conv.WorkspaceID, err)
		return false, false
	}
	return ws.ProjectID == conv.ProjectID, true
}

// Reconcile finishes, in the background, the conversions devpad stopped
// part way through, such as by a crash between attaching a workspace and
// rotating the key. Each project is marked as converting meanwhile, so it
// cannot be deleted under it. It also follows again the setup runs the
// previous server process was following.
func (s *service) Reconcile(ctx context.Context) error {
	conversions, err := s.repo.UnfinishedConversions(ctx)
	if err != nil {
		return err
	}
	for _, conv := range conversions {
		s.resumeInBackground(conv)
	}
	return s.resumeSetups(ctx)
}

// resumeInBackground resumes one conversion unless it is already running or
// its project is being deleted. It reports whether it started.
func (s *service) resumeInBackground(conv *Conversion) bool {
	s.mu.Lock()
	if s.converting[conv.ProjectID] || s.deleting[conv.ProjectID] {
		s.mu.Unlock()
		return false
	}
	s.converting[conv.ProjectID] = true
	s.mu.Unlock()

	s.background.Add(1)
	go func() {
		defer s.background.Done()
		defer s.endConverting(conv.ProjectID)
		ctx, cancel := context.WithTimeout(context.Background(), conversionFinishTimeout)
		defer cancel()
		unlock := s.lockWorkspace(conv.ProjectID)
		defer unlock()
		p, err := s.repo.GetByID(ctx, conv.ProjectID)
		if err != nil || p == nil {
			if err != nil {
				log.Printf("project %d: resuming conversion: %v", conv.ProjectID, err)
			}
			return
		}
		// Read it afresh: it may have moved on since it was listed.
		if fresh, err := s.repo.GetConversion(ctx, conv.ProjectID); err == nil && fresh != nil {
			conv = fresh
		}
		s.resumeConversion(ctx, p, conv)
	}()
	return true
}

// healConversion resumes the conversion of a project left "creating" by a
// step that failed, such as the write marking it ready.
func (s *service) healConversion(ctx context.Context, p *Project) {
	conv, err := s.repo.GetConversion(ctx, p.ID)
	if err != nil {
		log.Printf("project %d: reading conversion: %v", p.ID, err)
		return
	}
	if conv != nil && conv.State != ConversionDone {
		s.resumeInBackground(conv)
	}
}

// ConversionNotice returns what converting a workspace into the project did,
// until the user dismisses it; nil when there is nothing to show.
func (s *service) ConversionNotice(ctx context.Context, userID, projectID int64) (*Conversion, error) {
	p, err := s.Get(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	conv, err := s.repo.GetConversion(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if conv == nil || !conv.InPlace || conv.Dismissed {
		return nil, nil
	}
	if conv.State != ConversionDone {
		s.resumeInBackground(conv)
	}
	return conv, nil
}

// DismissConversionNotice stops showing what converting a workspace did.
func (s *service) DismissConversionNotice(ctx context.Context, userID, projectID int64) error {
	p, err := s.Get(ctx, userID, projectID)
	if err != nil {
		return err
	}
	conv, err := s.repo.GetConversion(ctx, p.ID)
	if err != nil || conv == nil {
		return err
	}
	conv.Dismissed = true
	return s.repo.UpdateConversion(ctx, conv)
}

// createConverting inserts a project being converted, with its conversion
// record, and marks it so under the same lock Delete checks, leaving no
// moment where it can be deleted.
func (s *service) createConverting(ctx context.Context, p *Project, conv *Conversion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.repo.CreateConverting(ctx, p, conv); err != nil {
		return err
	}
	s.converting[p.ID] = true
	return nil
}

func (s *service) endConverting(projectID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.converting, projectID)
}

// convertInput validates a conversion and returns the project to create and
// the cleaned repo path.
func (s *service) convertInput(userID int64, in ConvertInput) (*Project, string, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, "", invalid("name is required")
	}
	branch := strings.TrimSpace(in.DefaultBranch)
	if branch == "" {
		branch = DefaultBranch
	}
	if !validBranchName(branch) {
		return nil, "", invalid("defaultBranch is not a valid branch name")
	}
	upstream := strings.TrimSpace(in.UpstreamURL)
	if strings.HasPrefix(upstream, "-") {
		return nil, "", invalid("upstreamUrl must not start with '-'")
	}
	if hasURLCredentials(upstream) {
		return nil, "", invalid("upstreamUrl must not contain credentials; the project pushes with your SSH key")
	}
	maxSandboxes, err := normalizeMaxSandboxes(in.MaxSandboxes)
	if err != nil {
		return nil, "", err
	}
	repoPath := strings.TrimSpace(in.RepoPath)
	if repoPath != "" {
		repoPath = path.Clean(repoPath)
		if path.IsAbs(repoPath) || repoPath == ".." || strings.HasPrefix(repoPath, "../") || strings.HasPrefix(repoPath, "-") {
			return nil, "", invalid("repoPath must be inside the workspace")
		}
	}
	token, err := randomHex(32)
	if err != nil {
		return nil, "", fmt.Errorf("generating repo agent token: %w", err)
	}
	return &Project{
		UserID:         userID,
		Name:           name,
		Description:    in.Description,
		DefaultBranch:  branch,
		UpstreamURL:    upstream,
		SetupScript:    in.SetupScript,
		MaxSandboxes:   maxSandboxes,
		Status:         StatusCreating,
		RepoAgentToken: token,
	}, repoPath, nil
}

// importWorkspaceRepo moves every branch and tag of the workspace's repo
// into the project's new, empty canonical repo.
func (s *service) importWorkspaceRepo(ctx context.Context, p *Project, userID, workspaceID int64, repoPath string) error {
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return err
	}
	bundle, err := s.workspaces.BundleAllBranches(ctx, userID, workspaceID, repoPath)
	if err != nil {
		return fmt.Errorf("bundling the workspace's branches: %w", err)
	}
	defer bundle.Close()
	if _, err := c.RepoImportInitial(ctx, p.DefaultBranch, bundle); err != nil {
		return fmt.Errorf("importing the workspace's branches: %w", err)
	}
	return nil
}

// discardProject removes a project whose conversion failed, with its repo
// container. It is only called for a project still "creating", which
// nothing else can have used.
func (s *service) discardProject(ctx context.Context, p *Project) {
	if err := s.removeRepoContainer(ctx, p); err != nil {
		log.Printf("project %d: removing repo container after a failed conversion: %v", p.ID, err)
	}
	if err := s.repo.Delete(ctx, p.ID); err != nil {
		log.Printf("project %d: removing project after a failed conversion: %v", p.ID, err)
	}
}

// hasURLCredentials reports whether an upstream URL embeds a password or
// token. A bare SSH user, as in ssh://git@host/repo, is not one. The URL is
// split by hand, so one Go cannot parse still counts.
func hasURLCredentials(raw string) bool {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return false
	}
	authority, _, _ := strings.Cut(rest, "/")
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return false
	}
	_, _, hasPassword := strings.Cut(authority[:at], ":")
	return hasPassword || strings.HasPrefix(strings.ToLower(scheme), "http")
}

// sshKeyFingerprint identifies an SSH public key by its type and key data,
// ignoring the comment; empty for no key.
func sshKeyFingerprint(publicKey string) string {
	fields := strings.Fields(publicKey)
	if len(fields) < 2 {
		return ""
	}
	sum := sha256.Sum256([]byte(fields[0] + " " + fields[1]))
	return "sha256:" + hex.EncodeToString(sum[:])
}
