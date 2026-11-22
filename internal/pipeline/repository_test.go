package pipeline

import (
	"context"
	"maps"
	"slices"
	"strings"
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
	at := time.Now().UTC().Truncate(time.Second)
	if err := repo.FinishJob(ctx, a, JobLog{Output: []byte("last bytes"), Total: 12, Notes: []Note{{At: at, Text: "running the job"}}}); err != nil {
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
	j, told, err := repo.JobLog(ctx, p.ID, a.ID)
	if err != nil || string(told.Output) != "last bytes" || told.Total != 12 || j.ID != a.ID || len(told.Notes) != 1 || told.Notes[0].Text != "running the job" || !told.Notes[0].At.Equal(at) {
		t.Fatalf("log: %+v %+v %v", told, j, err)
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

// TestRepositoryQueriesUseTheirIndexes keeps every query that runs often,
// or over every pipeline there is, reading through an index: none scans the
// table or sorts what it read, so none grows with the pipelines kept.
func TestRepositoryQueriesUseTheirIndexes(t *testing.T) {
	e := setup(t)
	branch := e.branchPipeline("", "develop", sha(2))
	mr := MergeRequestKey{MergeRequestID: e.mr, HeadSHA: sha(30), TargetSHA: sha(2), ConfigHash: "h1"}.probe(e.project)
	query := func(q string, args []any) (string, []any) { return q, args }
	tests := []struct {
		name, query, index string
		args               []any
	}{
		{"the same branch pipeline", "", "idx_pipelines_branch_commit", nil},
		{"the same merge request pipeline", "", "idx_pipelines_merge_request_commits", nil},
		{"the project's pipelines", "", "idx_pipelines_project", nil},
		{"a page back", "", "idx_pipelines_project", nil},
		{"a branch's pipelines", "", "idx_pipelines_branch", nil},
		{"a merge request's pipelines", "", "idx_pipelines_merge_request", nil},
		{"the unfinished", listUnfinishedQuery, "idx_pipelines_unfinished", nil},
		{"the unannounced", listUnannouncedQuery, "idx_pipelines_unannounced", nil},
		{"the pinned", listPinnedFinishedQuery, "idx_pipelines_pinned", nil},
		{"those to prune", keptOutputsQuery, "idx_pipelines_keeping_output", []any{e.project, KeptOutputs}},
		{"the pipelines' jobs", jobsQuery(true, 2), "sqlite_autoindex_pipeline_jobs_2", []any{1, 2}},
		{"the latest of branches", "", "idx_pipelines_branch", nil},
		{"the latest of merge requests", "", "idx_pipelines_merge_request", nil},
	}
	tests[0].query, tests[0].args = findSameQuery(branch, countingStatuses)
	tests[1].query, tests[1].args = findSameQuery(mr, countingStatuses)
	tests[2].query, tests[2].args = query(listQuery(e.project, ListFilter{}))
	tests[3].query, tests[3].args = query(listQuery(e.project, ListFilter{Before: 100, Limit: 10}))
	tests[4].query, tests[4].args = query(listQuery(e.project, ListFilter{Branch: "develop"}))
	tests[5].query, tests[5].args = query(listQuery(e.project, ListFilter{MergeRequestID: e.mr}))
	tests[11].query, tests[11].args = query(latestQuery(e.project, LatestFilter{Branches: []string{"develop", "main"}}))
	tests[12].query, tests[12].args = query(latestQuery(e.project, LatestFilter{MergeRequestIDs: []int64{e.mr, e.mr + 1}}))
	for _, tc := range tests {
		rows, err := e.db.Query("EXPLAIN QUERY PLAN "+tc.query, tc.args...)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		all := strings.Join(plan, "; ")
		for _, step := range plan {
			indexed := strings.Contains(step, "INDEX") || strings.Contains(step, "USING INTEGER PRIMARY KEY")
			if strings.Contains(step, "TEMP B-TREE") || ((strings.HasPrefix(step, "SCAN ") || strings.HasPrefix(step, "SEARCH ")) && !indexed) {
				t.Errorf("%s reads more than it needs: %s", tc.name, all)
			}
		}
		if !strings.Contains(all, "INDEX "+tc.index+" ") && !strings.HasSuffix(all, "INDEX "+tc.index) {
			t.Errorf("%s does not use %s: %s", tc.name, tc.index, all)
		}
	}
}

func TestRepositoryPrunesTheOutputOfFinishedPipelinesPastTheNewest(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	repo := e.svc.repo
	// Three pipelines that started, the oldest still running, and one that
	// never did.
	var started []*Pipeline
	for i, uid := range []string{"f00d000000000001", "f00d000000000002", "f00d000000000003"} {
		p := e.branchPipeline(uid, "develop", sha(10+i))
		if err := repo.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		if ok, err := repo.MarkStarted(ctx, p.ID, "img", time.Now()); !ok || err != nil {
			t.Fatalf("start: %v %v", ok, err)
		}
		for _, j := range p.Jobs {
			if err := repo.SaveJobLog(ctx, j.ID, JobLog{Output: []byte("out"), Total: 3}); err != nil {
				t.Fatal(err)
			}
		}
		if i > 0 {
			finished := time.Now()
			p.Status, p.FinishedAt = StatusPassed, &finished
			if _, err := repo.Finish(ctx, p); err != nil {
				t.Fatal(err)
			}
		}
		started = append(started, p)
	}
	if err := repo.Create(ctx, e.branchPipeline("f00d000000000004", "develop", sha(20))); err != nil {
		t.Fatal(err)
	}
	kept := func(p *Pipeline) bool {
		got, _ := repo.Get(ctx, p.ID)
		return got.Jobs[0].OutputKept()
	}

	// The newest that started keeps its output; the next, finished, loses
	// it; the oldest is still running, so it keeps it until it finishes.
	if err := repo.PruneOutputs(ctx, e.project, 1); err != nil {
		t.Fatal(err)
	}
	if !kept(started[2]) || kept(started[1]) || !kept(started[0]) {
		t.Fatalf("kept: %v %v %v", kept(started[0]), kept(started[1]), kept(started[2]))
	}
	finished := time.Now()
	started[0].Status, started[0].FinishedAt = StatusPassed, &finished
	if _, err := repo.Finish(ctx, started[0]); err != nil {
		t.Fatal(err)
	}
	if err := repo.PruneOutputs(ctx, e.project, 1); err != nil {
		t.Fatal(err)
	}
	if kept(started[0]) || !kept(started[2]) {
		t.Fatalf("after the oldest finished, kept: %v %v", kept(started[0]), kept(started[2]))
	}
}

func TestRepositoryLatestIsEachSubjectsNewestOfTheProject(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	repo := e.svc.repo
	create := func(p *Pipeline) *Pipeline {
		t.Helper()
		if err := repo.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	mrPipeline := func(uid string, projectID, mrID int64, head string) *Pipeline {
		p := e.branchPipeline(uid, "", head)
		p.ProjectID, p.Kind, p.MergeRequestID, p.SourceBranch, p.TargetBranch, p.TargetSHA = projectID, KindMergeRequest, mrID, "feature", "develop", sha(2)
		return p
	}
	create(e.branchPipeline("a000000000000001", "develop", sha(2)))
	newestDevelop := create(e.branchPipeline("a000000000000002", "develop", sha(3)))
	main := create(e.branchPipeline("a000000000000003", "main", sha(1)))
	create(mrPipeline("a000000000000004", e.project, e.mr, sha(30)))
	newestMR := create(mrPipeline("a000000000000005", e.project, e.mr, sha(31)))

	// Another project's merge request is never this project's to read.
	res, err := e.db.Exec(`INSERT INTO projects (user_id, name, status) VALUES (?, 'other', 'ready')`, e.owner)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := res.LastInsertId()
	res, err = e.db.Exec(`INSERT INTO merge_requests (project_id, user_id, title, source_branch, target_branch, status, head_sha) VALUES (?, ?, 'x', 'feature', 'develop', 'open', ?)`, other, e.owner, sha(40))
	if err != nil {
		t.Fatal(err)
	}
	otherMR, _ := res.LastInsertId()
	create(mrPipeline("a000000000000006", other, otherMR, sha(40)))

	got, err := repo.Latest(ctx, e.project, LatestFilter{Branches: []string{"main", "develop", "gone"}, MergeRequestIDs: []int64{otherMR, e.mr}})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(got))
	for _, p := range got {
		ids = append(ids, p.ID)
	}
	if want := []int64{main.ID, newestDevelop.ID, newestMR.ID}; !slices.Equal(ids, want) {
		t.Fatalf("latest: %v, want %v", ids, want)
	}
	if len(got[0].Jobs) != 2 || got[0].Jobs[0].Run != "" {
		t.Fatalf("expected jobs without their scripts: %+v", got[0].Jobs)
	}
	if none, err := repo.Latest(ctx, e.project, LatestFilter{}); err != nil || len(none) != 0 {
		t.Fatalf("nothing asked: %v %v", none, err)
	}
}
