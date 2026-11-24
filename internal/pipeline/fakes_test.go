package pipeline

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/container"
	appdb "github.com/devpad-org/devpad/internal/database"
	"github.com/devpad-org/devpad/internal/pipeline/jobrun"
	"github.com/devpad-org/devpad/internal/pipelineid"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/wsservice"
)

// sha is a made-up full commit hash.
func sha(n int) string { return fmt.Sprintf("%040x", n) }

// fakeProjects is a project and its canonical repo: its branches, the files
// of its commits, and the refs pipelines pin in it.
type fakeProjects struct {
	mu       sync.Mutex
	project  project.Project
	branches map[string]string
	// files holds each commit's files, by path.
	files map[string]map[string]string
	// env is how the project's environment stands; envGate, when set, holds
	// AwaitEnvironment until it is closed.
	env     *project.Environment
	envErr  error
	envGate chan struct{}
	// conflict is what pinning a merge answers, when set.
	conflict *agent.MergeConflict
	pinErr   error
	pins     []agent.PipelinePin
	// refs are the pipelines' pins, by UID.
	refs    map[string]string
	deleted []string
	// branchesErr, when set, is what reading the branches fails with;
	// branchesGate, when set, holds each read until it is closed;
	// branchReads counts the reads.
	branchesErr  error
	branchesGate chan struct{}
	branchReads  int
	// changes are the files each head changes since it left its target,
	// by head; diffs and trialMerges count the reads of changes and of
	// trial merges.
	changes     map[string][]agent.RepoDiffFile
	diffs       int
	trialMerges int
	// diffErr, when set, is what reading a change fails with; pinShift,
	// when set, is the commit pinning a trial merge says it tested.
	diffErr  error
	pinShift string
}

func (f *fakeProjects) Get(_ context.Context, userID, projectID int64) (*project.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if projectID != f.project.ID {
		return nil, project.ErrNotFound
	}
	if userID != f.project.UserID {
		return nil, project.ErrForbidden
	}
	p := f.project
	return &p, nil
}

func (f *fakeProjects) Branches(ctx context.Context, _, _ int64) (*agent.RepoRefs, error) {
	f.mu.Lock()
	f.branchReads++
	gate := f.branchesGate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.branchesErr != nil {
		return nil, f.branchesErr
	}
	refs := &agent.RepoRefs{DefaultBranch: f.project.DefaultBranch}
	for name, sha := range f.branches {
		refs.Branches = append(refs.Branches, agent.RepoRef{Name: name, SHA: sha})
	}
	return refs, nil
}

func (f *fakeProjects) ReadAt(_ context.Context, _, _ int64, in agent.RepoReadRequest) (*agent.RepoRead, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := &agent.RepoRead{Commit: in.Rev}
	for _, path := range in.Files {
		content, ok := f.files[in.Rev][path]
		out.Files = append(out.Files, agent.RepoReadFile{Path: path, Found: ok, Content: []byte(content)})
	}
	return out, nil
}

func (f *fakeProjects) AwaitEnvironment(ctx context.Context, _, _ int64) (*project.Environment, error) {
	f.mu.Lock()
	gate, env, err := f.envGate, f.env, f.envErr
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return env, err
}

func (f *fakeProjects) ProjectEnvironment(context.Context, int64, int64) (*project.Environment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.env, f.envErr
}

func (f *fakeProjects) PinPipeline(_ context.Context, _, _ int64, pin agent.PipelinePin) (*agent.PipelinePinResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pins = append(f.pins, pin)
	if f.pinErr != nil {
		return nil, f.pinErr
	}
	if pin.Commit == "" && f.conflict != nil {
		delete(f.refs, pin.ID)
		return &agent.PipelinePinResult{Conflict: f.conflict}, nil
	}
	tested := pin.Commit
	if tested == "" {
		tested = mergeOf(pin.Source, pin.Target)
		if f.pinShift != "" {
			tested = f.pinShift
		}
	}
	f.refs[pin.ID] = tested
	return &agent.PipelinePinResult{Ref: pipelineid.Ref(pin.ID), SHA: tested, Tree: treeOf(tested)}, nil
}

func (f *fakeProjects) Diff(_ context.Context, _, _ int64, base, head string) (*agent.RepoDiff, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.diffs++
	if f.diffErr != nil {
		return nil, f.diffErr
	}
	return &agent.RepoDiff{BaseSHA: base, HeadSHA: head, MergeBase: base, Files: slices.Clone(f.changes[head])}, nil
}

func (f *fakeProjects) TrialMerge(_ context.Context, _, _ int64, source, target string) (*agent.TrialMerge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trialMerges++
	if f.conflict != nil {
		return &agent.TrialMerge{Conflict: f.conflict}, nil
	}
	merged := mergeOf(source, target)
	return &agent.TrialMerge{SHA: merged, Tree: treeOf(merged)}, nil
}

// setChange records the files head changes.
func (f *fakeProjects) setChange(head string, files ...agent.RepoDiffFile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.changes == nil {
		f.changes = map[string][]agent.RepoDiffFile{}
	}
	f.changes[head] = files
}

// treeOf is the tree the fake repo says a pinned commit has.
func treeOf(commit string) string {
	return "tree-" + commit[:12]
}

// mergeOf is the trial merge the fake repo makes of source onto target.
func mergeOf(source, target string) string {
	return "f" + source[1:20] + target[20:]
}

func (f *fakeProjects) PipelineBundle(_ context.Context, _, _ int64, uid string, w io.Writer) (int64, error) {
	f.mu.Lock()
	tested, ok := f.refs[uid]
	f.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("nothing is pinned for %s", uid)
	}
	n, err := io.WriteString(w, bundleOf(tested))
	return int64(n), err
}

// bundleOf is what the fake repo bundles for a tested commit.
func bundleOf(tested string) string { return "bundle of " + tested }

func (f *fakeProjects) DeletePipelineRef(_ context.Context, _, _ int64, uid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.refs, uid)
	f.deleted = append(f.deleted, uid)
	return nil
}

func (f *fakeProjects) ListPipelineProjects(context.Context) ([]*project.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.project.Status != project.StatusReady || len(f.project.PipelineBranches) == 0 {
		return nil, nil
	}
	p := f.project
	return []*project.Project{&p}, nil
}

// setProject changes the project as change says.
func (f *fakeProjects) setProject(change func(p *project.Project)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(&f.project)
}

// current is the project as it now is.
func (f *fakeProjects) current() *project.Project {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.project
	return &p
}

func (f *fakeProjects) setBranch(name, sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.branches[name] = sha
}

func (f *fakeProjects) setFile(commit, path, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.files[commit] == nil {
		f.files[commit] = map[string]string{}
	}
	f.files[commit][path] = content
}

func (f *fakeProjects) pinned() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.refs))
	for k, v := range f.refs {
		out[k] = v
	}
	return out
}

// fakeMergeRequests are the project's merge requests. While gate is open
// (non-nil and not closed), each lookup of one waits on it, and is counted
// in lookups.
type fakeMergeRequests struct {
	mu      sync.Mutex
	mrs     map[int64]*MergeRequest
	gate    chan struct{}
	lookups int
}

func (f *fakeMergeRequests) PipelineMergeRequest(ctx context.Context, _, _ int64, id int64) (*MergeRequest, error) {
	f.mu.Lock()
	f.lookups++
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	mr, ok := f.mrs[id]
	if !ok {
		return nil, nil
	}
	copied := *mr
	return &copied, nil
}

func (f *fakeMergeRequests) PipelineMergeRequestsInto(_ context.Context, _, _ int64, target string) ([]*MergeRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*MergeRequest
	for _, mr := range f.mrs {
		if mr.Open && mr.TargetBranch == target {
			copied := *mr
			out = append(out, &copied)
		}
	}
	slices.SortFunc(out, func(a, b *MergeRequest) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// set records a merge request as it now is.
func (f *fakeMergeRequests) set(mr MergeRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mrs[mr.ID] = &mr
}

// setHead moves a merge request's head.
func (f *fakeMergeRequests) setHead(id int64, head string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mrs[id].HeadSHA = head
}

// hold makes lookups wait until the returned func is called.
func (f *fakeMergeRequests) hold() (release func()) {
	gate := make(chan struct{})
	f.mu.Lock()
	f.gate = gate
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.gate = nil
		f.mu.Unlock()
		close(gate)
	}
}

func (f *fakeMergeRequests) lookupCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookups
}

// fakeDocker runs jobs' commands by what their scripts say:
//
//	pass          exits 0
//	echo TEXT     prints TEXT and exits 0
//	fail N        exits N
//	oom           exits 137 after the container ran out of memory
//	hang          runs until stopped
//	block [TEXT]  prints TEXT, runs until its job's gate opens, then prints
//	              "released" and exits 0
//	print N       prints N bytes and exits 0
type fakeDocker struct {
	mu         sync.Mutex
	networks   map[container.Job]bool
	containers map[string]*fakeContainer
	nextID     int
	// noPool is how many more networks Docker has no address pool for.
	noPool int
	// cloneFails makes cloning fail.
	cloneFails bool
	// leftovers are resources listed besides the jobs' own, such as another
	// devpad's.
	leftovers []container.JobResource
	removed   []container.Job
	// sweeps counts the listings of every pipeline's resources.
	sweeps int
	// clones are the clone commands run, by job name: jobs clone at once.
	clones  map[string][][]string
	scripts []string
	running int
	// maxRunning is the most scripts that ran at once.
	maxRunning int
	// latches hold block scripts, one per job name; allOpen opens every
	// one, those made later too.
	latches map[string]*scriptLatch
	allOpen bool
	// started receives each job's name as its script starts, while it has
	// room.
	started chan string
}

// scriptLatch holds the block scripts of one job name until it opens.
type scriptLatch struct {
	ch     chan struct{}
	opened bool
}

// latch is what holds the block scripts of jobs named name.
func (f *fakeDocker) latch(name string) chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latchLocked(name).ch
}

func (f *fakeDocker) latchLocked(name string) *scriptLatch {
	g := f.latches[name]
	if g == nil {
		g = &scriptLatch{ch: make(chan struct{})}
		f.latches[name] = g
		if f.allOpen {
			g.opened = true
			close(g.ch)
		}
	}
	return g
}

// open lets the block scripts of jobs named names end.
func (f *fakeDocker) open(names ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, name := range names {
		if g := f.latchLocked(name); !g.opened {
			g.opened = true
			close(g.ch)
		}
	}
}

// openAll lets every block script end, now and from now on.
func (f *fakeDocker) openAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allOpen = true
	for _, g := range f.latches {
		if !g.opened {
			g.opened = true
			close(g.ch)
		}
	}
}

type fakeContainer struct {
	job    container.Job
	spec   container.JobContainerSpec
	bundle string
}

func newFakeDocker() *fakeDocker {
	return &fakeDocker{
		networks:   map[container.Job]bool{},
		containers: map[string]*fakeContainer{},
		latches:    map[string]*scriptLatch{},
		clones:     map[string][][]string{},
		started:    make(chan string, 1000),
	}
}

func (f *fakeDocker) CreateJobNetwork(_ context.Context, job container.Job) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.noPool > 0 {
		f.noPool--
		return fmt.Errorf("creating the job's network: %w", container.ErrNoNetworkPool)
	}
	f.networks[job] = true
	return nil
}

func (f *fakeDocker) StartJobContainer(_ context.Context, spec container.JobContainerSpec) (string, error) {
	var bundle string
	for _, file := range spec.Files {
		b, err := io.ReadAll(file.Content)
		if err != nil {
			return "", err
		}
		if int64(len(b)) != file.Size {
			return "", fmt.Errorf("file %s holds %d bytes, not %d", file.Path, len(b), file.Size)
		}
		bundle = string(b)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.networks[spec.Job] {
		return "", errors.New("the job's network does not exist")
	}
	f.nextID++
	id := "c" + strconv.Itoa(f.nextID)
	f.containers[id] = &fakeContainer{job: spec.Job, spec: spec, bundle: bundle}
	return id, nil
}

func (f *fakeDocker) ExecJob(ctx context.Context, containerID string, spec container.ExecSpec, out io.Writer) (container.ExecResult, error) {
	f.mu.Lock()
	c := f.containers[containerID]
	if c == nil {
		f.mu.Unlock()
		return container.ExecResult{ExitCode: -1}, errors.New("no such container")
	}
	if spec.Cmd[0] == "git" {
		f.clones[c.job.Name] = append(f.clones[c.job.Name], spec.Cmd)
		fails := f.cloneFails
		f.mu.Unlock()
		if fails {
			fmt.Fprintln(out, "fatal: not a bundle")
			return container.ExecResult{ExitCode: 128}, nil
		}
		return container.ExecResult{}, nil
	}
	script := spec.Cmd[len(spec.Cmd)-1]
	f.scripts = append(f.scripts, script)
	f.running++
	f.maxRunning = max(f.maxRunning, f.running)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.running--
		f.mu.Unlock()
	}()
	word, arg, _ := strings.Cut(script, " ")
	if word == "block" && arg != "" {
		// Printed before the script is said to have started, so what a
		// blocked job printed is there to read.
		fmt.Fprintln(out, arg)
	}
	select {
	case f.started <- c.job.Name:
	default:
	}

	switch word {
	case "pass":
		return container.ExecResult{}, nil
	case "echo":
		fmt.Fprintln(out, arg)
		return container.ExecResult{}, nil
	case "fail":
		code, _ := strconv.Atoi(arg)
		fmt.Fprintln(out, "it broke")
		return container.ExecResult{ExitCode: code}, nil
	case "oom":
		return container.ExecResult{ExitCode: 137, OOMKilled: true}, nil
	case "print":
		n, _ := strconv.Atoi(arg)
		out.Write([]byte(strings.Repeat("x", n)))
		return container.ExecResult{}, nil
	case "block":
		select {
		case <-f.latch(c.job.Name):
			fmt.Fprintln(out, "released")
			return container.ExecResult{}, nil
		case <-ctx.Done():
			return container.ExecResult{ExitCode: -1}, context.Cause(ctx)
		}
	default: // hang
		<-ctx.Done()
		return container.ExecResult{ExitCode: -1}, context.Cause(ctx)
	}
}

func (f *fakeDocker) ListJobResources(_ context.Context, sel container.Job) ([]container.JobResource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sel == (container.Job{}) {
		f.sweeps++
	}
	return f.list(sel), nil
}

// list lists what Docker has of the jobs sel selects. The caller holds mu.
func (f *fakeDocker) list(sel container.Job) []container.JobResource {
	var out []container.JobResource
	for job := range f.networks {
		out = append(out, container.JobResource{Kind: container.JobResourceNetwork, Name: job.NetworkName(), Job: job})
	}
	for id, c := range f.containers {
		out = append(out, container.JobResource{Kind: container.JobResourceContainer, ID: id, Name: c.job.ContainerName(), Job: c.job})
	}
	out = append(out, f.leftovers...)
	var selected []container.JobResource
	for _, r := range out {
		if selects(sel, r.Job) {
			selected = append(selected, r)
		}
	}
	return selected
}

func (f *fakeDocker) RemoveJobResources(_ context.Context, sel container.Job) error {
	if sel.Pipeline == "" {
		return errors.New("removing job resources needs a pipeline")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, sel)
	for job := range f.networks {
		if selects(sel, job) {
			delete(f.networks, job)
		}
	}
	for id, c := range f.containers {
		if selects(sel, c.job) {
			delete(f.containers, id)
		}
	}
	kept := f.leftovers[:0]
	for _, r := range f.leftovers {
		if !selects(sel, r.Job) {
			kept = append(kept, r)
		}
	}
	f.leftovers = kept
	return nil
}

func selects(sel, job container.Job) bool {
	return (sel.Pipeline == "" || sel.Pipeline == job.Pipeline) && (sel.Name == "" || sel.Name == job.Name)
}

// left lists what Docker still has of pipeline uid, or of every pipeline.
func (f *fakeDocker) left(uid string) []container.JobResource {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.list(container.Job{Pipeline: uid})
}

// sweepsDone reports how many times every pipeline's resources were listed.
func (f *fakeDocker) sweepsDone() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sweeps
}

// fakeServices are the project's services, of which a job gets copies.
type fakeServices struct {
	mu      sync.Mutex
	have    map[wsservice.ServiceType]bool
	started []container.Job
	removed int
}

func (f *fakeServices) StartJobServices(_ context.Context, req wsservice.JobServicesRequest) (*wsservice.JobServices, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range req.Types {
		if !f.have[t] {
			return nil, fmt.Errorf("%s: %w", t, wsservice.ErrNotProjectService)
		}
	}
	f.started = append(f.started, req.Job)
	var env []string
	for _, t := range req.Types {
		env = append(env, strings.ToUpper(string(t))+"_HOST="+string(t))
	}
	return &wsservice.JobServices{Env: env}, nil
}

func (f *fakeServices) RemoveJobServices(context.Context, *wsservice.JobServices) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed++
	return nil
}

// recordingListener collects the pipelines it is told finished, and the
// branches whose awaited runs started no pipeline.
type recordingListener struct {
	finished    chan *Pipeline
	startedNone chan string
}

func (l *recordingListener) PipelineFinished(_ context.Context, p *Pipeline) {
	l.finished <- p
}

func (l *recordingListener) BranchRunStartedNone(_ context.Context, _, _ int64, branch string) {
	l.startedNone <- branch
}

// env is a pipeline service over a real database, with fakes for the rest:
// a project with protected branches main and develop, and an open merge
// request from feature into develop.
type env struct {
	t        *testing.T
	db       *sql.DB
	svc      *service
	projects *fakeProjects
	mrs      *fakeMergeRequests
	docker   *fakeDocker
	services *fakeServices
	listener *recordingListener
	owner    int64
	stranger int64
	project  int64
	mr       int64
	tempDir  string
}

const jobsYAML = "jobs:\n  build:\n    run: echo built\n  test:\n    run: pass\n"

func setup(t *testing.T) *env {
	t.Helper()
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	conn := db.Conn()
	exec := func(query string, args ...any) int64 {
		t.Helper()
		res, err := conn.Exec(query, args...)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	e := &env{t: t, db: conn, tempDir: t.TempDir()}
	e.owner = exec(`INSERT INTO users (username, email, password) VALUES ('owner', 'owner@example.com', 'x')`)
	e.stranger = exec(`INSERT INTO users (username, email, password) VALUES ('stranger', 's@example.com', 'x')`)
	e.project = exec(`INSERT INTO projects (user_id, name, status) VALUES (?, 'shop', 'ready')`, e.owner)
	e.mr = exec(`INSERT INTO merge_requests (project_id, user_id, title, source_branch, target_branch, status, head_sha) VALUES (?, ?, 'Cart', 'feature', 'develop', 'open', ?)`, e.project, e.owner, sha(30))

	e.projects = &fakeProjects{
		project: project.Project{
			ID: e.project, UserID: e.owner, Name: "shop", DefaultBranch: "main",
			ProtectedBranches: []string{"develop"}, Status: project.StatusReady,
		},
		branches: map[string]string{"main": sha(1), "develop": sha(2), "feature": sha(30)},
		files:    map[string]map[string]string{},
		refs:     map[string]string{},
	}
	for _, commit := range []string{sha(1), sha(2)} {
		e.projects.setFile(commit, ".devpad/pipeline.yml", jobsYAML)
	}
	e.mrs = &fakeMergeRequests{mrs: map[int64]*MergeRequest{
		e.mr: {ID: e.mr, SourceBranch: "feature", TargetBranch: "develop", HeadSHA: sha(30), Open: true},
	}}
	e.docker = newFakeDocker()
	e.services = &fakeServices{have: map[wsservice.ServiceType]bool{wsservice.ServicePostgres: true}}
	e.listener = &recordingListener{finished: make(chan *Pipeline, 100), startedNone: make(chan string, 100)}
	e.svc = e.newService()
	t.Cleanup(func() { e.shutdown() })
	return e
}

// newService makes a pipeline service over the env's database and fakes,
// as a devpad starting afresh would.
func (e *env) newService() *service {
	jobs := jobrun.New(e.docker, e.services, jobrun.Config{NetworkRetry: 5 * time.Millisecond, NetworkWait: 5 * time.Second, CloneTimeout: 5 * time.Second})
	svc := NewService(Deps{
		Repo:          NewRepository(e.db),
		Projects:      e.projects,
		MergeRequests: e.mrs,
		Jobs:          jobs,
		Docker:        e.docker,
		BundleDir:     e.tempDir,
	}).(*service)
	svc.AddListener(e.listener)
	// Automatic runs of a key go at once in tests, but for those of the
	// interval itself.
	svc.auto.interval = 0
	return svc
}

func (e *env) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.svc.Shutdown(ctx); err != nil {
		e.t.Errorf("shutdown: %v", err)
	}
}

// start starts a pipeline as the owner, failing the test if it cannot.
func (e *env) start(in StartInput) *Pipeline {
	e.t.Helper()
	in.UserID, in.ProjectID = e.owner, e.project
	if in.Trigger == "" {
		in.Trigger = TriggerUser
	}
	p, _, err := e.svc.Start(context.Background(), in)
	if err != nil {
		e.t.Fatalf("start %+v: %v", in, err)
	}
	return p
}

// wait waits for a pipeline to finish and returns it as recorded.
func (e *env) wait(id int64) *Pipeline {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		p, err := e.svc.repo.Get(context.Background(), id)
		if err != nil {
			e.t.Fatalf("get %d: %v", id, err)
		}
		if p != nil && p.Status.Finished() {
			e.waitIdle(p.UID)
			return p
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("pipeline %d still %s", id, p.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitIdle waits until the service no longer runs the pipeline uid names.
func (e *env) waitIdle(uid string) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for e.svc.hasRun(uid) {
		if time.Now().After(deadline) {
			e.t.Fatalf("pipeline %s is still running", uid)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// awaitScript waits for a job's script to start.
func (e *env) awaitScript(name string) {
	e.t.Helper()
	if got := e.nextScript(); got != name {
		e.t.Fatalf("job %s started, want %s", got, name)
	}
}

// nextScript waits for the next job's script to start, and returns the
// job's name.
func (e *env) nextScript() string {
	e.t.Helper()
	select {
	case got := <-e.docker.started:
		return got
	case <-time.After(10 * time.Second):
		e.t.Fatal("no job's script started")
		return ""
	}
}

// job returns a pipeline's job by name.
func job(t *testing.T, p *Pipeline, name string) *JobRun {
	t.Helper()
	for _, j := range p.Jobs {
		if j.Name == name {
			return j
		}
	}
	t.Fatalf("pipeline %d has no job %s", p.ID, name)
	return nil
}

// output reads all that is kept of a job's output.
func (e *env) output(p *Pipeline, name string) string {
	e.t.Helper()
	return string(e.read(p, name).Data)
}

// notes reads devpad's notes on a job.
func (e *env) notes(p *Pipeline, name string) []string {
	e.t.Helper()
	var texts []string
	for _, n := range e.read(p, name).Notes {
		texts = append(texts, n.Text)
	}
	return texts
}

func (e *env) read(p *Pipeline, name string) *Output {
	e.t.Helper()
	out, err := e.svc.JobOutput(context.Background(), e.owner, e.project, p.ID, OutputRequest{JobID: job(e.t, p, name).ID})
	if err != nil {
		e.t.Fatalf("output of %s: %v", name, err)
	}
	return out
}

// tempFiles lists the files left in the env's temporary directory.
func (e *env) tempFiles() []string {
	entries, _ := os.ReadDir(e.tempDir)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
