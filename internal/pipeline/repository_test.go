package pipeline

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"
)

// branchPipeline is a queued pipeline of a branch with one job, to record.
func (e *env) branchPipeline(uid, branch, head string) *Pipeline {
	return &Pipeline{
		ProjectID: e.project, UserID: e.owner, UID: uid, Kind: KindBranch, Branch: branch, HeadSHA: head,
		ConfigSource: SourceFile, ConfigHash: "h1", Status: StatusQueued, Trigger: TriggerUser, CreatedAt: time.Now().UTC(),
		Jobs: []*JobRun{
			{Job: Job{Name: "b", Run: "make b", Timeout: 90 * time.Second, Services: []string{"postgres"}}, Position: 1, Status: JobQueued},
			{Job: Job{Name: "a", Run: "make a", Timeout: 1500 * time.Millisecond}, Position: 0, Status: JobQueued},
		},
	}
}

func TestRepositoryRecordsAPipelineThroughItsRun(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	repo := e.svc.repo
	p := e.branchPipeline("c0ffee0000000001", "develop", sha(2))
	if err := repo.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, p.ID)
	if err != nil || got.UID != p.UID || got.Status != StatusQueued || len(got.Jobs) != 2 {
		t.Fatalf("get: %+v %v", got, err)
	}
	a, b := got.Jobs[0], got.Jobs[1]
	if a.Name != "a" || a.Timeout != 1500*time.Millisecond || a.Services != nil || b.Name != "b" || !slices.Equal(b.Services, []string{"postgres"}) || b.Run != "make b" {
		t.Fatalf("jobs: %+v %+v", a, b)
	}

	if ok, err := repo.MarkStarted(ctx, p.ID, "img", time.Now()); !ok || err != nil {
		t.Fatalf("start: %v %v", ok, err)
	}
	if ok, _ := repo.MarkStarted(ctx, p.ID, "other", time.Now()); ok {
		t.Fatal("started twice")
	}
	if err := repo.StartJob(ctx, a.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	code, finished := 2, time.Now()
	a.Status, a.ExitCode, a.Error, a.OutputBytes, a.FinishedAt = JobFailed, &code, "exited 2", 12, &finished
	if err := repo.FinishJob(ctx, a, []byte("last bytes")); err != nil {
		t.Fatal(err)
	}
	p.Status, p.FinishedAt, p.ConflictPaths = StatusFailed, &finished, nil
	if ok, err := repo.Finish(ctx, p); !ok || err != nil {
		t.Fatalf("finish: %v %v", ok, err)
	}
	p.Status = StatusPassed
	if ok, _ := repo.Finish(ctx, p); ok {
		t.Fatal("finished twice")
	}

	got, _ = repo.Get(ctx, p.ID)
	if got.Status != StatusFailed || got.Image != "img" || got.StartedAt == nil || got.FinishedAt == nil {
		t.Fatalf("finished: %+v", got)
	}
	if a := got.Jobs[0]; a.Status != JobFailed || *a.ExitCode != 2 || a.OutputBytes != 12 || a.KeptBytes != 10 {
		t.Fatalf("job a: %+v", a)
	}
	// The job that never started was skipped with its pipeline.
	if b := got.Jobs[1]; b.Status != JobSkipped || b.StartedAt != nil || b.FinishedAt == nil || b.ExitCode != nil {
		t.Fatalf("job b: %+v", b)
	}
	j, output, err := repo.JobOutput(ctx, p.ID, a.ID)
	if err != nil || string(output) != "last bytes" || j.ID != a.ID {
		t.Fatalf("output: %q %+v %v", output, j, err)
	}
}

func TestRepositoryRefusesAPipelineOfNeitherKind(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mixed := e.branchPipeline("c0ffee0000000002", "develop", sha(2))
	mixed.MergeRequestID = e.mr
	if err := e.svc.repo.Create(ctx, mixed); err == nil {
		t.Fatal("recorded a branch pipeline of a merge request")
	}
	noTarget := e.branchPipeline("c0ffee0000000003", "", sha(30))
	noTarget.Kind, noTarget.MergeRequestID = KindMergeRequest, e.mr
	if err := e.svc.repo.Create(ctx, noTarget); err == nil {
		t.Fatal("recorded a merge request pipeline with no target commit")
	}
	dup := e.branchPipeline("c0ffee0000000004", "develop", sha(2))
	if err := e.svc.repo.Create(ctx, dup); err != nil {
		t.Fatal(err)
	}
	again := e.branchPipeline("c0ffee0000000004", "main", sha(1))
	if err := e.svc.repo.Create(ctx, again); err == nil {
		t.Fatal("recorded two pipelines with one UID")
	}
}

func TestRepositoryFindsTheSamePipeline(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	repo := e.svc.repo
	p := e.branchPipeline("c0ffee0000000005", "develop", sha(2))
	if err := repo.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	probe := e.branchPipeline("", "develop", sha(2))
	if same, err := repo.FindSame(ctx, probe, []Status{StatusQueued}); err != nil || same == nil || same.ID != p.ID {
		t.Fatalf("same: %+v %v", same, err)
	}
	for name, change := range map[string]func(*Pipeline){
		"another commit": func(q *Pipeline) { q.HeadSHA = sha(3) },
		"other jobs":     func(q *Pipeline) { q.ConfigHash = "h2" },
		"another branch": func(q *Pipeline) { q.Branch = "main" },
	} {
		q := e.branchPipeline("", "develop", sha(2))
		change(q)
		if same, _ := repo.FindSame(ctx, q, []Status{StatusQueued}); same != nil {
			t.Errorf("%s: found %d", name, same.ID)
		}
	}
	if same, _ := repo.FindSame(ctx, probe, []Status{StatusPassed}); same != nil {
		t.Errorf("found a queued pipeline as passed")
	}

	// A merge request's pipeline is the same only for the same pair of
	// commits and jobs.
	mr := &Pipeline{ProjectID: e.project, UserID: e.owner, UID: "c0ffee0000000008", Kind: KindMergeRequest, MergeRequestID: e.mr, HeadSHA: sha(30), TargetSHA: sha(2), ConfigSource: SourceFile, ConfigHash: "h1", Status: StatusPassed, Trigger: TriggerUser, CreatedAt: time.Now().UTC()}
	if err := repo.Create(ctx, mr); err != nil {
		t.Fatal(err)
	}
	key := MergeRequestKey{MergeRequestID: e.mr, HeadSHA: sha(30), TargetSHA: sha(2), ConfigHash: "h1"}
	if same, err := repo.FindSame(ctx, key.probe(e.project), countingStatuses); err != nil || same == nil || same.ID != mr.ID {
		t.Fatalf("same merge request pipeline: %+v %v", same, err)
	}
	for name, change := range map[string]func(*MergeRequestKey){
		"another head":   func(k *MergeRequestKey) { k.HeadSHA = sha(31) },
		"another target": func(k *MergeRequestKey) { k.TargetSHA = sha(1) },
		"other jobs":     func(k *MergeRequestKey) { k.ConfigHash = "h2" },
	} {
		k := key
		change(&k)
		if same, _ := repo.FindSame(ctx, k.probe(e.project), countingStatuses); same != nil {
			t.Errorf("%s: found %d", name, same.ID)
		}
	}
	if same, _ := repo.FindSame(ctx, key.probe(e.project+1), countingStatuses); same != nil {
		t.Errorf("found another project's pipeline")
	}
}

func TestRepositoryInterruptsUnfinishedPipelines(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	repo := e.svc.repo
	queued := e.branchPipeline("c0ffee0000000006", "develop", sha(2))
	done := e.branchPipeline("c0ffee0000000007", "main", sha(1))
	done.Status, done.RefPinned = StatusPassed, true
	for _, p := range []*Pipeline{queued, done} {
		if err := repo.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	// Job a, the file's first, was running.
	if err := repo.StartJob(ctx, queued.Jobs[1].ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	unfinished, err := repo.ListUnfinished(ctx)
	if err != nil || len(unfinished) != 1 || unfinished[0].ID != queued.ID {
		t.Fatalf("unfinished: %+v %v", unfinished, err)
	}
	// One that finished since it was listed is left as it is.
	done.Status = StatusQueued
	interrupted, err := repo.Interrupt(ctx, []*Pipeline{unfinished[0], done}, "stopped", time.Now())
	if err != nil || len(interrupted) != 1 || interrupted[0].ID != queued.ID || interrupted[0].Status != StatusInterrupted {
		t.Fatalf("interrupted: %+v %v", interrupted, err)
	}
	got, _ := repo.Get(ctx, queued.ID)
	if got.Status != StatusInterrupted || got.Error != "stopped" || got.Jobs[0].Status != JobInterrupted || got.Jobs[1].Status != JobSkipped {
		t.Fatalf("got %+v %+v %+v", got, got.Jobs[0], got.Jobs[1])
	}
	records, err := repo.RecordsByUID(ctx, []string{queued.UID, done.UID, "unknown"})
	want := map[string]UIDRecord{queued.UID: {ProjectID: e.project, Status: StatusInterrupted}, done.UID: {ProjectID: e.project, Status: StatusPassed}}
	if err != nil || !maps.Equal(records, want) {
		t.Fatalf("records: %v %v", records, err)
	}
	pinned, err := repo.ListPinnedFinished(ctx)
	if err != nil || len(pinned) != 1 || pinned[0].ID != done.ID {
		t.Fatalf("pinned: %+v %v", pinned, err)
	}
}
