package mergerequest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/auth"
)

// fakePipelines stands in for the pipeline service: the gate from gate, and
// reports and job scripts from reports, when a test has any.
type fakePipelines struct {
	gate    *fakeGate
	reports *fakeReports
}

func (f fakePipelines) MergeGate(ctx context.Context, userID, projectID, mrID int64) (*Gate, error) {
	return f.gate.MergeGate(ctx, userID, projectID, mrID)
}

func (f fakePipelines) PipelineReport(ctx context.Context, userID, projectID, pipelineID int64, tail int) (*PipelineReport, error) {
	if f.reports == nil {
		return nil, errors.New("no pipeline reports in this test")
	}
	return f.reports.PipelineReport(ctx, userID, projectID, pipelineID, tail)
}

func (f fakePipelines) JobScripts(ctx context.Context, userID, projectID int64, branch string) ([]string, error) {
	if f.reports == nil {
		return nil, nil
	}
	return f.reports.JobScripts(ctx, userID, projectID, branch)
}

// usePipelines has the service read the env's gate, and reports, when
// there are any.
func (e *env) usePipelines(reports *fakeReports) {
	e.svc.SetPipelines(fakePipelines{gate: e.gate, reports: reports})
}

// fakeGate stands in for the pipeline gate: it answers gate, unguarded and
// clear unless a test sets another, or err.
type fakeGate struct {
	mu   sync.Mutex
	gate Gate
	err  error
	// asked counts the questions, and onAsk runs as each is asked.
	asked int
	onAsk func()
}

func (f *fakeGate) MergeGate(context.Context, int64, int64, int64) (*Gate, error) {
	f.mu.Lock()
	f.asked++
	onAsk := f.onAsk
	g, err := f.gate, f.err
	f.mu.Unlock()
	if onAsk != nil {
		onAsk()
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (f *fakeGate) set(g Gate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gate = g
}

func (f *fakeGate) questions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked
}

// passedOn is the gate of a merge request whose pipeline, number id,
// passed on head merged onto target.
func passedOn(id int64, head, target string) Gate {
	return Gate{State: "passed", Guarded: true, Clear: true, PipelineID: id, HeadSHA: head, TargetSHA: target, TestedTree: "tree-" + target}
}

// shut is a gate a pipeline keeps shut.
func shut(state GateState, reason string, waits bool, id int64) Gate {
	return Gate{State: state, Guarded: true, Waits: waits, Overridable: true, Reason: reason, PipelineID: id, HeadSHA: "head1", TargetSHA: mainTip}
}

// pipelineRow records a pipeline of the merge request, for an override to
// name.
func (e *env) pipelineRow(t *testing.T, mrID int64, status string) int64 {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO pipelines (project_id, user_id, uid, kind, merge_request_id, head_sha, target_sha, config_source, status, trigger, created_at)
		VALUES (?, ?, ?, 'merge_request', ?, 'head1', ?, 'file', ?, 'opened', ?)`,
		e.pid(), e.owner, fmt.Sprintf("p%d", time.Now().UnixNano()), mrID, mainTip, status, time.Now())
	if err != nil {
		t.Fatalf("pipeline row: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// mrLockHeld reports whether the merge request's lock is held, by anyone.
func (e *env) mrLockHeld(id int64) bool {
	e.svc.mu.Lock()
	l := e.svc.locks[fmt.Sprintf("mr:%d", id)]
	e.svc.mu.Unlock()
	if l == nil || l.TryLock() {
		if l != nil {
			l.Unlock()
		}
		return false
	}
	return true
}

// The user's merge goes through the gate: an unguarded or passed merge
// request merges, a passed one only onto the target commit its pipeline
// tested, and anything else is refused, saying how the gate stands.
func TestUserMergeAsksThePipelineGate(t *testing.T) {
	tests := []struct {
		name   string
		gate   Gate
		merges bool
		// target is where the merge must find the target, "" for anywhere.
		target string
	}{
		// Unguarded, it still lands only on the target commit the gate read.
		{name: "no pipeline guards it", gate: Gate{State: "unguarded", Clear: true, HeadSHA: "head1", TargetSHA: mainTip}, merges: true, target: mainTip},
		{name: "its pipeline passed", gate: passedOn(3, "head1", mainTip), merges: true, target: mainTip},
		{name: "queued", gate: shut("queued", "pipeline #3 is still queued", true, 3)},
		{name: "running", gate: shut("running", "pipeline #3 is still running", true, 3)},
		{name: "not run yet", gate: shut("not_run", "no pipeline has run on it yet", true, 0)},
		{name: "out of date", gate: shut("out_of_date", "the latest pipeline, #2, is out of date", true, 2)},
		{name: "failed", gate: shut("failed", "pipeline #3 failed (backend)", false, 3)},
		{name: "doesn't merge", gate: shut("conflict", "pipeline #3 found the branch doesn't merge onto main", false, 3)},
		{name: "invalid", gate: shut("invalid", "main's pipeline doesn't parse: line 2", false, 0)},
		{name: "couldn't run", gate: shut("error", "pipeline #3 couldn't run: no Docker", false, 3)},
		{name: "cancelled", gate: shut("cancelled", "pipeline #3 was cancelled", false, 3)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := autoMergeEnv(t)
			ctx := context.Background()
			mr := e.openTaskMR(t)
			e.gate.set(tt.gate)
			e.gate.onAsk = func() {
				if !e.mrLockHeld(mr.ID) {
					t.Error("the gate must be asked under the merge request's lock")
				}
			}
			_, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{})
			if e.gate.questions() != 1 {
				t.Fatalf("expected the gate asked once, got %d", e.gate.questions())
			}
			got, _ := e.svc.repo.GetByID(ctx, mr.ID)
			if !tt.merges {
				var gateErr *GateError
				if !errors.As(err, &gateErr) || !errors.Is(err, ErrPipelineGate) || gateErr.Gate.State != tt.gate.State || !strings.Contains(err.Error(), tt.gate.Reason) {
					t.Fatalf("expected the gate's refusal, got %v", err)
				}
				if e.mergeCount() != 0 || got.Status != StatusOpen {
					t.Fatalf("a refused merge must not reach the repo: %+v, %+v", e.projects.merges, got)
				}
				return
			}
			if err != nil || got.Status != StatusMerged || got.PipelineOverride != nil {
				t.Fatalf("expected a merge with nothing overridden: %v, %+v", err, got)
			}
			// Only a pass has a tree the merge must land.
			wantTree := ""
			if tt.gate.State == "passed" {
				wantTree = "tree-" + tt.target
			}
			if in := e.projects.merges[0]; in.ExpectedTargetSHA != tt.target || in.ExpectedSourceSHA != "head1" || in.ExpectedTree != wantTree {
				t.Fatalf("merge input = %+v", in)
			}
		})
	}
}

// Nothing merges while devpad can't tell how the gate stands.
func TestMergeWithoutAGateIsRefused(t *testing.T) {
	e := autoMergeEnv(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	e.gate.mu.Lock()
	e.gate.err = errors.New("the repo is down")
	e.gate.mu.Unlock()
	if _, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{OverridePipeline: &GateConfirmation{State: "failed"}}); err == nil || !strings.Contains(err.Error(), "the repo is down") {
		t.Fatalf("expected the gate's error, got %v", err)
	}
	e.svc.SetPipelines(nil)
	if _, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{}); err == nil {
		t.Fatal("expected no merge without a gate")
	}
	if e.mergeCount() != 0 {
		t.Fatalf("expected no merge, got %+v", e.projects.merges)
	}
}

// The user can merge over a shut gate only by confirming it as it stands
// now, and the merge request records that they did and how the gate stood.
func TestUserOverridesThePipelineGate(t *testing.T) {
	e := autoMergeEnv(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	failed := e.pipelineRow(t, mr.ID, "failed")
	g := shut("failed", fmt.Sprintf("pipeline #%d failed (backend)", failed), false, failed)
	e.gate.set(g)
	seen := g.confirmation()

	result, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{OverridePipeline: &seen})
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	want := &PipelineOverride{State: "failed", PipelineID: failed}
	if o := result.MergeRequest.PipelineOverride; o == nil || *o != *want || result.MergeRequest.MergedBy != MergedByUser {
		t.Fatalf("expected the override recorded, got %+v", result.MergeRequest)
	}
	got, _ := e.svc.repo.GetByID(ctx, mr.ID)
	if o := got.PipelineOverride; o == nil || *o != *want || got.MergedBy != MergedByUser {
		t.Fatalf("expected the override stored, got %+v", got)
	}
	// It merges onto the target commit the gate read, as a pass would, but
	// with no tree to land: nothing passed to have tested one.
	if in := e.projects.merges[0]; in.ExpectedTargetSHA != mainTip || in.ExpectedTree != "" {
		t.Fatalf("merge input = %+v", in)
	}
}

// An override binds to what the user saw: the same state with another
// pipeline, another head, another target, or a pipeline where there was
// none, is asked again, and nothing merges.
func TestAnOverrideOfWhatTheGateNoLongerIsIsAskedAgain(t *testing.T) {
	seen := shut("failed", "pipeline #3 failed (backend)", false, 3)
	notRun := shut("not_run", "no pipeline has run on it yet", true, 0)
	for _, tt := range []struct {
		name      string
		confirmed Gate
		now       func(g Gate) Gate
	}{
		{name: "another state", confirmed: seen, now: func(g Gate) Gate { g.State = "running"; return g }},
		{name: "another pipeline", confirmed: seen, now: func(g Gate) Gate { g.PipelineID = 4; return g }},
		{name: "another head", confirmed: seen, now: func(g Gate) Gate { g.HeadSHA = "head2"; return g }},
		{name: "another target", confirmed: seen, now: func(g Gate) Gate { g.TargetSHA = "moved"; return g }},
		{name: "a pipeline where there was none", confirmed: notRun, now: func(g Gate) Gate { g.State, g.PipelineID = "running", 4; return g }},
		{name: "none where there was one", confirmed: seen, now: func(Gate) Gate { return notRun }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := autoMergeEnv(t)
			ctx := context.Background()
			mr := e.openTaskMR(t)
			now := tt.now(tt.confirmed)
			if now.HeadSHA != "head1" {
				// The head moves, as a task's round would, under the lock.
				got, _ := e.svc.repo.GetByID(ctx, mr.ID)
				if err := e.svc.repo.UpdateHead(ctx, got, now.HeadSHA, PushTask); err != nil {
					t.Fatal(err)
				}
			}
			e.gate.set(now)
			confirmed := tt.confirmed.confirmation()
			_, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{OverridePipeline: &confirmed})
			var gateErr *GateError
			if !errors.As(err, &gateErr) || !gateErr.Changed || gateErr.Gate.confirmation() != now.confirmation() || e.mergeCount() != 0 {
				t.Fatalf("expected the user asked again with the gate as it is, got %v", err)
			}
		})
	}
}

// A clear gate needs no override, and records none; a merge request that
// can no longer merge, or whose branch doesn't merge onto its target,
// can't be overridden into merging.
func TestOverrideOnlyWhereTheGateIsShutByItsPipeline(t *testing.T) {
	t.Run("clear", func(t *testing.T) {
		e := autoMergeEnv(t)
		mr := e.openTaskMR(t)
		e.gate.set(passedOn(3, "head1", mainTip))
		result, err := e.svc.Merge(context.Background(), e.owner, e.pid(), mr.ID, MergeInput{OverridePipeline: &GateConfirmation{State: "failed"}})
		if err != nil || result.MergeRequest.PipelineOverride != nil {
			t.Fatalf("expected a plain merge, got %+v %v", result, err)
		}
	})
	for _, g := range []Gate{
		{State: "closed", Guarded: true, Reason: "it can no longer merge into a protected branch"},
		// The merge would refuse just as the pipeline's trial merge did.
		{State: "conflict", Guarded: true, Reason: "pipeline #3 found the branch doesn't merge onto main", PipelineID: 3, HeadSHA: "head1", TargetSHA: mainTip},
	} {
		t.Run(string(g.State), func(t *testing.T) {
			e := autoMergeEnv(t)
			mr := e.openTaskMR(t)
			e.gate.set(g)
			confirmed := g.confirmation()
			_, err := e.svc.Merge(context.Background(), e.owner, e.pid(), mr.ID, MergeInput{OverridePipeline: &confirmed})
			var gateErr *GateError
			if !errors.As(err, &gateErr) || gateErr.Changed || e.mergeCount() != 0 {
				t.Fatalf("expected no merge, got %v", err)
			}
			if g.State == "conflict" && !strings.Contains(err.Error(), "resolve the conflicts") {
				t.Fatalf("expected the conflict to be pointed at, got %v", err)
			}
		})
	}
}

// Only a merge by the user can have overridden the gate, over a state it
// can be overridden in, and only an override names a pipeline: the
// database holds whatever writes it to that.
func TestOnlyTheUsersMergeRecordsAnOverride(t *testing.T) {
	e := autoMergeEnv(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	pipelineID := e.pipelineRow(t, mr.ID, "failed")
	now := time.Now()
	mr.Status, mr.MergeSHA, mr.MergedAt, mr.MergedBy = StatusMerged, "mergecommit", &now, MergedByAuto
	mr.PipelineOverride = &PipelineOverride{State: "failed"}
	if err := e.svc.repo.Update(ctx, mr); err == nil {
		t.Fatal("expected an automatic merge refused an override")
	}
	mr.MergedBy = MergedByUser
	for _, state := range []GateState{GatePassed, GateUnguarded, GateConflict, GateClosed, "nonsense"} {
		mr.PipelineOverride = &PipelineOverride{State: state}
		if err := e.svc.repo.Update(ctx, mr); err == nil {
			t.Fatalf("expected an override of %q refused", state)
		}
	}
	if _, err := e.db.Exec(`UPDATE merge_requests SET pipeline_override_id = ? WHERE id = ?`, pipelineID, mr.ID); err == nil {
		t.Fatal("expected a pipeline named without an override refused")
	}
	mr.PipelineOverride = &PipelineOverride{State: "failed", PipelineID: pipelineID}
	if err := e.svc.repo.Update(ctx, mr); err != nil {
		t.Fatalf("the user's: %v", err)
	}
}

// The merge endpoint refuses a shut gate with how it stands, and merges
// over it when the user confirms it exactly as it stands.
func TestHandlerMergesOverTheGate(t *testing.T) {
	e := autoMergeEnv(t)
	mr := e.openTaskMR(t)
	e.gate.set(shut("running", "pipeline #3 is still running", true, 0))
	h := NewHandler(e.svc)
	serve := func(body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.SetPathValue("id", itoa(e.pid()))
		req.SetPathValue("mrId", itoa(mr.ID))
		req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: e.owner}))
		rec := httptest.NewRecorder()
		h.HandleMerge(rec, req)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := serve(`{"strategy":"merge"}`)
	gate, _ := out["pipelineGate"].(map[string]any)
	if code != http.StatusConflict || gate["state"] != "running" || gate["overridable"] != true || gate["changed"] != false || !strings.Contains(fmt.Sprint(out["error"]), "still running") ||
		gate["pipelineId"] != nil || gate["headSha"] != "head1" || gate["targetSha"] != mainTip {
		t.Fatalf("refusal: %d %v", code, out)
	}
	// Confirming another head is asked again, with the gate as it is.
	code, out = serve(`{"strategy":"merge","overridePipeline":{"state":"running","pipelineId":null,"headSha":"head0","targetSha":"` + mainTip + `"}}`)
	if gate, _ := out["pipelineGate"].(map[string]any); code != http.StatusConflict || gate["changed"] != true || gate["headSha"] != "head1" {
		t.Fatalf("a stale confirmation: %d %v", code, out)
	}
	code, out = serve(`{"strategy":"merge","overridePipeline":{"state":"running","pipelineId":null,"headSha":"head1","targetSha":"` + mainTip + `"}}`)
	got, _ := out["mergeRequest"].(map[string]any)
	override, _ := got["pipelineOverride"].(map[string]any)
	if code != http.StatusOK || override["state"] != "running" || override["pipelineId"] != nil {
		t.Fatalf("override: %d %v", code, out)
	}
}

// The tree that passed is the tree that lands: a target that moves between
// the gate and the repo's merge refuses the merge, saying the pipeline must
// pass on the target as it is now. Unguarded, the target may have gained a
// pipeline meanwhile, so the move is refused too.
func TestUserMergeRefusedWhenTheTargetMovesAsItMerges(t *testing.T) {
	t.Run("unguarded", func(t *testing.T) {
		e := autoMergeEnv(t)
		mr := e.openTaskMR(t)
		e.gate.set(Gate{State: "unguarded", Clear: true, HeadSHA: "head1", TargetSHA: mainTip})
		e.projects.onMerge = func() {
			e.projects.mu.Lock()
			e.projects.branches[0].SHA = "addspipeline"
			e.projects.mu.Unlock()
		}
		_, err := e.svc.Merge(context.Background(), e.owner, e.pid(), mr.ID, MergeInput{})
		var moved *targetMovedError
		if !errors.As(err, &moved) || moved.guarded || !strings.Contains(err.Error(), "merge again onto main as it is now") {
			t.Fatalf("expected the target's move refused, got %v", err)
		}
	})
	e := autoMergeEnv(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	e.gate.set(passedOn(3, "head1", mainTip))
	e.projects.onMerge = func() {
		e.projects.mu.Lock()
		e.projects.branches[0].SHA = "othermerge"
		e.projects.mu.Unlock()
	}
	_, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{})
	var moved *targetMovedError
	if !errors.As(err, &moved) || !moved.guarded || !errors.Is(err, ErrInvalidState) || !strings.Contains(err.Error(), "must pass on main as it is now") {
		t.Fatalf("expected the target's move refused, got %v", err)
	}
	if got, _ := e.svc.repo.GetByID(ctx, mr.ID); got.Status != StatusOpen {
		t.Fatalf("expected the merge request left open, got %+v", got)
	}
	// A refusal for anything else, the target where the gate read it, is
	// the repo's own.
	e.projects.onMerge = nil
	e.projects.branches[0].SHA = mainTip
	e.projects.mergeErr = &agent.AgentError{StatusCode: http.StatusConflict, Message: "task/1 conflicts with main"}
	if _, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{}); errors.As(err, &moved) || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected the repo's refusal, got %v", err)
	}
}

// An automatic merge needs the gate clear, never overrides it, and when a
// pipeline guards the target and passed on the target as it is now, a
// target that moved after the review no longer stops it.
func TestAutoMergeAsksThePipelineGate(t *testing.T) {
	tests := []struct {
		name string
		gate Gate
		// moved moves main after the review read the change.
		moved  bool
		merges bool
		target string
		why    string
	}{
		{name: "passed on the target the review read", gate: passedOn(3, "head1", mainTip), merges: true, target: mainTip},
		{name: "passed on the target as it moved since", gate: passedOn(3, "head1", "moved"), moved: true, merges: true, target: "moved"},
		{name: "unguarded, the target moved", gate: Gate{State: "unguarded", Clear: true}, moved: true, why: "main moved after this review read the change"},
		{name: "unguarded", gate: Gate{State: "unguarded", Clear: true}, merges: true, target: mainTip},
		{name: "failed, the target moved", gate: shut("failed", "pipeline #3 failed (backend)", false, 3), moved: true, why: "pipeline #3 failed (backend)"},
		{name: "failed", gate: shut("failed", "pipeline #3 failed (backend)", false, 3), why: "pipeline #3 failed (backend)"},
		{name: "cancelled", gate: shut("cancelled", "pipeline #3 was cancelled", false, 3), why: "pipeline #3 was cancelled"},
		{name: "invalid", gate: shut("invalid", "main's pipeline doesn't parse: line 2", false, 0), why: "main's pipeline doesn't parse: line 2"},
		{name: "couldn't run", gate: shut("error", "pipeline #3 couldn't run: no Docker", false, 3), why: "pipeline #3 couldn't run: no Docker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := autoMergeEnv(t)
			ctx := context.Background()
			mr := e.openTaskMR(t)
			rv := e.startAgentReview(t, mr, AgentReviewInput{})
			e.gate.set(tt.gate)
			e.approve(t, rv, domain.AgentRunCompleted, func() {
				if tt.moved {
					e.projects.mu.Lock()
					e.projects.branches[0].SHA = "moved"
					e.projects.mu.Unlock()
				}
			})
			note := e.settled(t, rv)
			got, _ := e.svc.repo.GetByID(ctx, mr.ID)
			if merged := got.Status == StatusMerged; merged != tt.merges || note.AutoMergeError != tt.why {
				t.Fatalf("merged %v (want %v), note %q (want %q)", merged, tt.merges, note.AutoMergeError, tt.why)
			}
			if got.PipelineOverride != nil {
				t.Fatalf("an automatic merge must never override the gate: %+v", got.PipelineOverride)
			}
			if tt.merges {
				in := e.projects.merges[0]
				wantTree := ""
				if tt.gate.Guarded {
					wantTree = "tree-" + tt.target
				}
				if in.ExpectedTargetSHA != tt.target || in.ExpectedTree != wantTree || got.MergedBy != MergedByAuto {
					t.Fatalf("merge input = %+v", in)
				}
			}
			if !tt.merges && e.mergeCount() != 0 {
				t.Fatalf("expected no merge, got %+v", e.projects.merges)
			}
		})
	}
}

// waitingApproval has a review agent approve the task's merge request while
// its pipeline runs, and waits until devpad has decided to wait for it.
func (e *env) waitingApproval(t *testing.T) (*MergeRequest, *Review) {
	t.Helper()
	mr := e.openTaskMR(t)
	rv := e.startAgentReview(t, mr, AgentReviewInput{})
	e.gate.set(shut("running", "pipeline #3 is still running", true, 3))
	e.approve(t, rv, domain.AgentRunCompleted, nil)
	waitFor(t, "the approval to ask the gate", func() bool { return e.gate.questions() > 0 })
	got, _ := e.svc.repo.GetReview(context.Background(), rv.ID)
	if !got.Finishing() || got.AutoMergeError != "" || e.mergeCount() != 0 {
		t.Fatalf("expected the approval waiting, got %+v", got)
	}
	return mr, rv
}

func (e *env) pipelineFinished(mr *MergeRequest) {
	e.svc.PipelineFinished(context.Background(), PipelineEnd{UserID: mr.UserID, ProjectID: mr.ProjectID, MergeRequestID: mr.ID})
}

// An approval whose run ends while the pipeline runs waits for it, and
// merges once it passes, onto the target commit it passed on.
func TestAutoMergeWaitsForThePipelineThenMerges(t *testing.T) {
	e := autoMergeEnv(t)
	ctx := context.Background()
	mr, rv := e.waitingApproval(t)
	// Told of a pipeline that hasn't let it through yet, it waits on.
	e.pipelineFinished(mr)
	waitFor(t, "the gate asked again", func() bool { return e.gate.questions() > 1 })

	// Meanwhile another merge moved main, and the pipeline ran again on it.
	e.projects.mu.Lock()
	e.projects.branches[0].SHA = "moved"
	e.projects.mu.Unlock()
	e.gate.set(passedOn(4, "head1", "moved"))
	e.pipelineFinished(mr)
	if got := e.settled(t, rv); got.AutoMergeError != "" {
		t.Fatalf("unexpected refusal: %q", got.AutoMergeError)
	}
	got, _ := e.svc.repo.GetByID(ctx, mr.ID)
	if got.Status != StatusMerged || got.MergedBy != MergedByAuto || e.projects.merges[0].ExpectedTargetSHA != "moved" {
		t.Fatalf("expected the merge onto the commit that passed, got %+v, %+v", got, e.projects.merges)
	}
	// Being told again merges nothing twice.
	e.pipelineFinished(mr)
	e.svc.Shutdown(ctx)
	if e.mergeCount() != 1 {
		t.Fatalf("expected one merge, got %+v", e.projects.merges)
	}
}

// A pipeline that fails ends the wait: the approval is settled, refused
// with why.
func TestAutoMergeWaitsForThePipelineThenRefuses(t *testing.T) {
	e := autoMergeEnv(t)
	mr, rv := e.waitingApproval(t)
	e.gate.set(shut("failed", "pipeline #3 failed (backend)", false, 3))
	e.pipelineFinished(mr)
	if got := e.settled(t, rv); got.AutoMergeError != "pipeline #3 failed (backend)" {
		t.Fatalf("expected the failure recorded, got %q", got.AutoMergeError)
	}
	if got, _ := e.svc.repo.GetByID(context.Background(), mr.ID); got.Status != StatusOpen || e.mergeCount() != 0 {
		t.Fatalf("expected the merge request left open, got %+v", got)
	}
}

// What else happened while the approval waited still refuses it when the
// pipeline ends, even with a pass.
func TestAWaitingApprovalStillNeedsEverythingElse(t *testing.T) {
	e := autoMergeEnv(t)
	mr, rv := e.waitingApproval(t)
	if err := e.svc.repo.CreateComment(context.Background(), &Comment{MergeRequestID: mr.ID, Author: AuthorUser, Body: "No.", HeadSHA: "head1", Blocking: true}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	e.gate.set(passedOn(3, "head1", mainTip))
	e.pipelineFinished(mr)
	if got := e.settled(t, rv); got.AutoMergeError != "a blocking thread is open" || e.mergeCount() != 0 {
		t.Fatalf("expected the blocking thread to refuse it, got %q", got.AutoMergeError)
	}
}

// A target that moves as the repo merges leaves the approval waiting, for
// the pipeline that the move runs again.
func TestAutoMergeWaitsWhenTheTargetMovesAsItMerges(t *testing.T) {
	e := autoMergeEnv(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	rv := e.startAgentReview(t, mr, AgentReviewInput{})
	e.gate.set(passedOn(3, "head1", mainTip))
	e.projects.onMerge = func() {
		e.projects.mu.Lock()
		e.projects.branches[0].SHA = "othermerge"
		e.projects.onMerge = nil
		e.projects.mu.Unlock()
	}
	e.approve(t, rv, domain.AgentRunCompleted, nil)
	waitFor(t, "the repo asked to merge", func() bool { return e.mergeCount() == 1 })
	e.svc.Shutdown(ctx)
	if got, _ := e.svc.repo.GetReview(ctx, rv.ID); !got.Finishing() || got.AutoMergeError != "" {
		t.Fatalf("expected the approval waiting, got %+v", got)
	}

	// After a restart, its pipeline passed on the new commit.
	e.restart(t)
	e.gate.set(passedOn(4, "head1", "othermerge"))
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := e.settled(t, rv); got.AutoMergeError != "" {
		t.Fatalf("unexpected refusal: %q", got.AutoMergeError)
	}
	if got, _ := e.svc.repo.GetByID(ctx, mr.ID); got.Status != StatusMerged || e.projects.merges[1].ExpectedTargetSHA != "othermerge" {
		t.Fatalf("expected the merge onto the commit that passed, got %+v %+v", got, e.projects.merges)
	}
}

// restart replaces the service with a new one over the same database and
// fakes, as devpad starting again does.
func (e *env) restart(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.svc.Shutdown(ctx)
	e.svc = NewService(Deps{
		Repo: NewRepository(e.db), Projects: e.projects, Workspaces: e.workspaces, Tasks: e.tasks, Runs: e.runs,
		Conversations: fakeConversations{e.db}, Defaults: e.defaults, Budgets: e.budgets, Models: e.models,
	}).(*service)
	e.runs.onFinish = e.svc.RunFinished
	e.usePipelines(nil)
	svc := e.svc
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		svc.Shutdown(ctx)
	})
}

// An approval left waiting when devpad stopped, whose pipeline then
// finished, merges as devpad starts again; one whose pipeline failed is
// refused.
func TestReconcileSettlesWaitingApprovals(t *testing.T) {
	for _, tt := range []struct {
		name string
		gate Gate
		why  string
	}{
		{name: "passed", gate: passedOn(3, "head1", mainTip)},
		{name: "failed", gate: shut("failed", "pipeline #3 failed (backend)", false, 3), why: "pipeline #3 failed (backend)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := autoMergeEnv(t)
			ctx := context.Background()
			mr, rv := e.waitingApproval(t)
			e.restart(t)
			e.gate.set(tt.gate)
			if err := e.svc.Reconcile(ctx); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if got := e.settled(t, rv); got.AutoMergeError != tt.why {
				t.Fatalf("expected the note to say %q, got %q", tt.why, got.AutoMergeError)
			}
			if got, _ := e.svc.repo.GetByID(ctx, mr.ID); (got.Status == StatusMerged) != (tt.why == "") {
				t.Fatalf("unexpected merge request: %+v", got)
			}
		})
	}
}

// A waiting approval that another review has since followed is settled as
// no longer the latest when a pipeline ends, rather than left waiting.
func TestAnOlderWaitingApprovalIsSettledWhenThePipelineEnds(t *testing.T) {
	e := autoMergeEnv(t)
	ctx := context.Background()
	mr, rv := e.waitingApproval(t)
	if _, err := e.svc.Review(ctx, e.owner, e.pid(), mr.ID, ReviewInput{Verdict: VerdictComment, Body: "Hold on."}); err != nil {
		t.Fatalf("user review: %v", err)
	}
	e.gate.set(passedOn(3, "head1", mainTip))
	e.pipelineFinished(mr)
	if got := e.settled(t, rv); got.AutoMergeError != "another review started after this one" || e.mergeCount() != 0 {
		t.Fatalf("expected it refused, got %q", got.AutoMergeError)
	}
}

// No review or task agent tool merges, so none can override the gate:
// the tools a review agent and a task's agent get are these, and only the
// user's merge endpoint reaches Merge.
func TestNoAgentToolMerges(t *testing.T) {
	e := autoMergeEnv(t)
	mr := e.openTaskMR(t)
	rv := e.startAgentReview(t, mr, AgentReviewInput{})
	allowed := map[string]bool{commentTool: true, resolveTool: true, submitTool: true, readCommentsTool: true, mrCommentTool: true}
	for _, ws := range []int64{rv.WorkspaceID, e.taskSandbox(t)} {
		tools := e.contribution(e.owner, ws).Tools
		if len(tools) == 0 {
			t.Fatalf("workspace %d is offered no tools to check", ws)
		}
		for _, tool := range tools {
			if !allowed[tool.Function.Name] {
				t.Errorf("workspace %d offers %s: a new tool must not merge", ws, tool.Function.Name)
			}
		}
	}
}

// A change to the pipeline file never merges automatically, even with its
// pipeline passed: merged on an agent's approval, it would weaken or remove
// the gate of every merge request after it. The user merges it.
func TestAutoMergeNeverTakesAChangeToThePipelineFile(t *testing.T) {
	for _, tt := range []struct {
		name string
		file agent.RepoDiffFile
		why  string
	}{
		{name: "adding it", file: agent.RepoDiffFile{Path: ".devpad/pipeline.yml", Status: "A"}, why: ".devpad/pipeline.yml"},
		{name: "changing it", file: agent.RepoDiffFile{Path: ".devpad/pipeline.yaml", Status: "M"}, why: ".devpad/pipeline.yaml"},
		{name: "deleting it", file: agent.RepoDiffFile{Path: ".devpad/pipeline.yml", Status: "D"}, why: ".devpad/pipeline.yml"},
		{name: "renaming it away", file: agent.RepoDiffFile{Path: "ci/pipeline.yml", OldPath: ".devpad/pipeline.yml", Status: "R"}, why: ".devpad/pipeline.yml"},
		{name: "renaming another onto it", file: agent.RepoDiffFile{Path: ".devpad/pipeline.yml", OldPath: "ci/pipeline.yml", Status: "R"}, why: ".devpad/pipeline.yml"},
		{name: "another file of .devpad", file: agent.RepoDiffFile{Path: ".devpad/skills/x/SKILL.md", Status: "A"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := autoMergeEnv(t)
			ctx := context.Background()
			e.projects.files = append(e.projects.files, tt.file)
			mr := e.openTaskMR(t)
			rv := e.startAgentReview(t, mr, AgentReviewInput{})
			e.gate.set(passedOn(3, "head1", mainTip))
			e.approve(t, rv, domain.AgentRunCompleted, nil)
			note := e.settled(t, rv)
			got, _ := e.svc.repo.GetByID(ctx, mr.ID)
			if tt.why == "" {
				if got.Status != StatusMerged || note.AutoMergeError != "" {
					t.Fatalf("expected a merge, got %+v, %q", got, note.AutoMergeError)
				}
				return
			}
			want := "it changes " + tt.why + ", which devpad never merges by itself"
			if got.Status != StatusOpen || e.mergeCount() != 0 || note.AutoMergeError != want {
				t.Fatalf("expected it left for the user, saying %q; got %+v, %q", want, got, note.AutoMergeError)
			}
			// The user merges it.
			if _, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{}); err != nil {
				t.Fatalf("the user's merge: %v", err)
			}
		})
	}
}

// A merge that skips the gate is refused rather than made.
func TestAMergeWithoutAnAdmissionIsRefused(t *testing.T) {
	e := autoMergeEnv(t)
	mr := e.openTaskMR(t)
	if _, err := e.svc.merge(context.Background(), e.owner, e.pid(), mr.ID, mergeOptions{by: MergedByUser}); err == nil || e.mergeCount() != 0 {
		t.Fatalf("expected no merge, got %v", err)
	}
}

// An approval waiting for its pipeline is decided again as soon as the
// project's settings change: unticking the target's pipeline leaves it to
// the review's own rule, and taking the target off the automatic merges
// ends the wait, so it neither waits for a pipeline that will never run
// nor merges at the next start under rules that no longer hold.
func TestSettingsChangesDecideWaitingApprovalsAgain(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(e *env)
		merges bool
	}{
		{name: "the pipeline unticked", change: func(e *env) {
			e.gate.set(Gate{State: "unguarded", Clear: true, HeadSHA: "head1", TargetSHA: mainTip})
		}, merges: true},
		{name: "no longer merged into automatically", change: func(e *env) {
			e.projects.project.AutoMergeBranches = nil
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := autoMergeEnv(t)
			ctx := context.Background()
			mr, rv := e.waitingApproval(t)
			before := e.projects.project
			// Another project's change decides nothing here.
			other := before
			other.ID++
			e.svc.ProjectSettingsChanged(ctx, &before, &other)
			e.svc.Shutdown(ctx)
			if got, _ := e.svc.repo.GetReview(ctx, rv.ID); !got.Finishing() {
				t.Fatalf("another project's change settled it: %+v", got)
			}
			e.restart(t)

			tt.change(e)
			after := e.projects.project
			e.svc.ProjectSettingsChanged(ctx, &before, &after)
			if got := e.settled(t, rv); got.AutoMergeError != "" {
				t.Fatalf("unexpected refusal: %q", got.AutoMergeError)
			}
			if got, _ := e.svc.repo.GetByID(ctx, mr.ID); (got.Status == StatusMerged) != tt.merges {
				t.Fatalf("merged %v, want %v", got.Status == StatusMerged, tt.merges)
			}
		})
	}
}

// Closing or merging a merge request settles the approvals still waiting
// on it, which can no longer merge it.
func TestEndingAMergeRequestSettlesItsWaitingApprovals(t *testing.T) {
	t.Run("closed", func(t *testing.T) {
		e := autoMergeEnv(t)
		ctx := context.Background()
		mr, rv := e.waitingApproval(t)
		if _, err := e.svc.Close(ctx, e.owner, e.pid(), mr.ID); err != nil {
			t.Fatalf("close: %v", err)
		}
		if got, _ := e.svc.repo.GetReview(ctx, rv.ID); got.Finishing() || got.AutoMergeError != "" {
			t.Fatalf("expected it settled, saying nothing: %+v", got)
		}
	})
	t.Run("merged by the user", func(t *testing.T) {
		e := autoMergeEnv(t)
		ctx := context.Background()
		mr, rv := e.waitingApproval(t)
		g := shut("running", "pipeline is still running", true, e.pipelineRow(t, mr.ID, "running"))
		e.gate.set(g)
		running := g.confirmation()
		if _, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{OverridePipeline: &running}); err != nil {
			t.Fatalf("merge: %v", err)
		}
		if got, _ := e.svc.repo.GetReview(ctx, rv.ID); got.Finishing() || got.AutoMergeError != "" {
			t.Fatalf("expected it settled, saying nothing: %+v", got)
		}
	})
}
