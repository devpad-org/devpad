package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
	appdb "github.com/devpad-org/devpad/internal/database"
)

func TestAgentRunRepository_PersistsRunsAndOrderedEvents(t *testing.T) {
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	ctx := context.Background()
	userID := insertAgentRunTestUser(t, db)
	workspaceID := insertAgentRunTestWorkspace(t, db, userID)
	repo := NewAgentRunRepository(db.Conn())

	thinkingEnabled := true
	run := &domain.AgentRun{
		UserID:      userID,
		WorkspaceID: workspaceID,
		Model:       "gpt-5.4",
		Status:      domain.AgentRunQueued,
		InputTurns:  []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
		Thinking:    &domain.ThinkingConfig{Enabled: &thinkingEnabled},
	}
	if err := repo.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if run.ID == 0 {
		t.Fatal("expected run ID to be assigned")
	}

	stored, err := repo.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if stored == nil {
		t.Fatal("expected stored run")
	}
	if stored.UserID != userID || stored.WorkspaceID != workspaceID || stored.Model != "gpt-5.4" {
		t.Fatalf("unexpected stored run: %+v", stored)
	}
	if len(stored.InputTurns) != 1 || stored.InputTurns[0].Text() != "hello" {
		t.Fatalf("unexpected stored turns: %+v", stored.InputTurns)
	}
	if stored.Thinking == nil || stored.Thinking.Enabled == nil || !*stored.Thinking.Enabled {
		t.Fatalf("unexpected stored thinking config: %+v", stored.Thinking)
	}
	listedRuns, err := repo.ListRuns(ctx, userID, workspaceID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(listedRuns) != 1 || listedRuns[0].ID != run.ID {
		t.Fatalf("unexpected listed runs: %+v", listedRuns)
	}

	first, err := repo.AppendEvent(ctx, run.ID, domain.ClientEvent{TextDelta: "first"})
	if err != nil {
		t.Fatalf("append first event: %v", err)
	}
	second, err := repo.AppendEvent(ctx, run.ID, domain.ClientEvent{Done: true})
	if err != nil {
		t.Fatalf("append second event: %v", err)
	}
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("unexpected event sequences: %d, %d", first.Sequence, second.Sequence)
	}

	events, err := repo.ListEvents(ctx, run.ID, 1)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].Sequence != 2 || !events[0].Event.Done {
		t.Fatalf("unexpected listed events: %+v", events)
	}

	if err := repo.UpdateStatus(ctx, run.ID, domain.AgentRunWaitingUser, ""); err != nil {
		t.Fatalf("update waiting user status: %v", err)
	}
	waiting, err := repo.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get waiting user run: %v", err)
	}
	if waiting.Status != domain.AgentRunWaitingUser || waiting.StartedAt == nil {
		t.Fatalf("expected waiting user run with started timestamp, got %+v", waiting)
	}

	if err := repo.UpdateStatus(ctx, run.ID, domain.AgentRunCompleted, ""); err != nil {
		t.Fatalf("update status: %v", err)
	}
	completed, err := repo.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get completed run: %v", err)
	}
	if completed.Status != domain.AgentRunCompleted || completed.CompletedAt == nil {
		t.Fatalf("expected completed run, got %+v", completed)
	}
}

func TestAgentRunRepository_AppendsEventsFromConcurrentRuns(t *testing.T) {
	db, err := appdb.Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	userID := insertAgentRunTestUser(t, db)
	workspaceID := insertAgentRunTestWorkspace(t, db, userID)
	repo := NewAgentRunRepository(db.Conn())

	const (
		runCount     = 8
		eventsPerRun = 25
	)
	runs := make([]*domain.AgentRun, 0, runCount)
	for i := 0; i < runCount; i++ {
		run := &domain.AgentRun{
			UserID:      userID,
			WorkspaceID: workspaceID,
			Model:       "gpt-5.4",
			Status:      domain.AgentRunQueued,
			InputTurns:  []domain.Turn{domain.NewTextTurn(domain.RoleUser, fmt.Sprintf("run %d", i))},
		}
		if err := repo.CreateRun(ctx, run); err != nil {
			t.Fatalf("create run %d: %v", i, err)
		}
		runs = append(runs, run)
	}

	start := make(chan struct{})
	errs := make(chan error, runCount)
	var wg sync.WaitGroup
	for _, run := range runs {
		run := run
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for eventIndex := 0; eventIndex < eventsPerRun; eventIndex++ {
				if _, err := repo.AppendEvent(ctx, run.ID, domain.ClientEvent{
					TextDelta: fmt.Sprintf("run %d event %d", run.ID, eventIndex),
				}); err != nil {
					errs <- fmt.Errorf("append event for run %d: %w", run.ID, err)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, run := range runs {
		events, err := repo.ListEvents(ctx, run.ID, 0)
		if err != nil {
			t.Fatalf("list events for run %d: %v", run.ID, err)
		}
		if len(events) != eventsPerRun {
			t.Fatalf("expected %d events for run %d, got %d", eventsPerRun, run.ID, len(events))
		}
		for i, event := range events {
			wantSequence := int64(i + 1)
			if event.Sequence != wantSequence {
				t.Fatalf("expected sequence %d for run %d event %d, got %d", wantSequence, run.ID, i, event.Sequence)
			}
		}
	}
}

func insertAgentRunTestUser(t *testing.T, db *appdb.DB) int64 {
	t.Helper()
	result, err := db.Conn().Exec(
		`INSERT INTO users (username, email, password)
		 VALUES (?, ?, ?)`,
		"agent-runner",
		"agent-runner@example.com",
		"hashed-password",
	)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("get user id: %v", err)
	}
	return id
}

func insertAgentRunTestWorkspace(t *testing.T, db *appdb.DB, userID int64) int64 {
	t.Helper()
	result, err := db.Conn().Exec(
		`INSERT INTO workspaces (user_id, name, status, image)
		 VALUES (?, ?, ?, ?)`,
		userID,
		"agent-runner-workspace",
		"stopped",
		"ubuntu:22.04",
	)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("get workspace id: %v", err)
	}
	return id
}
