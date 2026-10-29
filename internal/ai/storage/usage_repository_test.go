package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
	appdb "github.com/devpad-org/devpad/internal/database"
)

func TestUsageRepositoryKeepsUsageAfterTheProjectIsGone(t *testing.T) {
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Conn().Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO users (id, username, email, password) VALUES (1, 'a', 'a@example.com', 'x'), (2, 'b', 'b@example.com', 'x')`)
	exec(`INSERT INTO projects (id, user_id, name, status) VALUES (1, 1, 'shop', 'ready'), (2, 1, 'blog', 'ready')`)
	exec(`INSERT INTO workspaces (id, user_id, name, project_id, kind, branch) VALUES (1, 1, 'shop', 1, 'task', 'cart'), (2, 1, 'blog', 2, 'interactive', 'main')`)

	repo := NewUsageRepository(db.Conn())
	cost := func(v float64) *float64 { return &v }
	at := time.Date(2026, 10, 28, 9, 7, 0, 0, time.UTC)
	for _, rec := range []domain.UsageRecord{
		{UserID: 1, WorkspaceID: 1, RunID: 7, Model: "opus", Usage: domain.Usage{InputTokens: 100, OutputTokens: 10, CostUSD: cost(0.5)}, At: at},
		{UserID: 1, WorkspaceID: 1, RunID: 7, Model: "opus", Usage: domain.Usage{InputTokens: 50, CacheReadTokens: 400, CostUSD: cost(0.25)}, At: at.Add(5 * time.Minute)},
		{UserID: 1, WorkspaceID: 2, Model: "local", Usage: domain.Usage{InputTokens: 30}, At: at},
		{UserID: 1, Model: "opus", Usage: domain.Usage{OutputTokens: 1, CostUSD: cost(0.01)}, At: at.Add(time.Hour)},
		// User 2 names user 1's workspace, which says nothing of its project.
		{UserID: 2, WorkspaceID: 2, Model: "opus", Usage: domain.Usage{OutputTokens: 1, CostUSD: cost(1)}, At: at},
	} {
		if err := repo.RecordUsage(ctx, rec); err != nil {
			t.Fatalf("record %+v: %v", rec, err)
		}
	}

	// The task's sandbox and then its project go; the ledger keeps both.
	exec(`DELETE FROM workspaces WHERE id = 1`)
	exec(`DELETE FROM projects WHERE id = 1`)
	exec(`UPDATE projects SET name = 'journal' WHERE id = 2`)

	slots, err := repo.ListUsageSlots(ctx, 1, at.Add(-time.Hour), at.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	quarter := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	// User 2's request is not user 1's.
	want := []domain.UsageSlot{
		{Start: quarter, ProjectID: 1, ProjectName: "shop", ProjectDeleted: true, Model: "opus",
			Totals: domain.UsageTotals{Requests: 2, InputTokens: 150, OutputTokens: 10, CacheReadTokens: 400, CostUSD: 0.75}},
		{Start: quarter, ProjectID: 2, ProjectName: "journal", Model: "local",
			Totals: domain.UsageTotals{Requests: 1, UnpricedRequests: 1, InputTokens: 30}},
		{Start: quarter.Add(time.Hour), ProjectID: 0, Model: "opus",
			Totals: domain.UsageTotals{Requests: 1, OutputTokens: 1, CostUSD: 0.01}},
	}
	byKey := func(s domain.UsageSlot) string { return fmt.Sprintf("%s/%d/%s", s.Start, s.ProjectID, s.Model) }
	gotByKey := map[string]domain.UsageSlot{}
	for _, s := range slots {
		gotByKey[byKey(s)] = s
	}
	if len(slots) != len(want) {
		t.Fatalf("slots = %+v, want %+v", slots, want)
	}
	for _, w := range want {
		if g, ok := gotByKey[byKey(w)]; !ok || g != w {
			t.Fatalf("slot %s = %+v, want %+v", byKey(w), g, w)
		}
	}

	everyone, err := repo.ListUsageSlots(ctx, 0, at.Add(-time.Hour), at.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("list everyone: %v", err)
	}
	if len(everyone) != len(want)+1 {
		t.Fatalf("expected user 2's request too, got %+v", everyone)
	}

	others, err := repo.ListUsageSlots(ctx, 2, at.Add(-time.Hour), at.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("list user 2: %v", err)
	}
	if len(others) != 1 || others[0].ProjectID != 0 || others[0].ProjectName != "" {
		t.Fatalf("expected user 2's request under no project, got %+v", others)
	}
	var foreign int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM ai_usage WHERE user_id = 2 AND (workspace_id IS NOT NULL OR workspace_kind != '')`).Scan(&foreign); err != nil || foreign != 0 {
		t.Fatalf("expected user 2's row to keep nothing of the workspace, got %d %v", foreign, err)
	}

	// The range's end is not included.
	if slots, err := repo.ListUsageSlots(ctx, 1, at.Add(-time.Hour), at.Add(-7*time.Minute)); err != nil || len(slots) != 0 {
		t.Fatalf("expected nothing before 09:00, got %+v %v", slots, err)
	}
}
