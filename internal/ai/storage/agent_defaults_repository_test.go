package storage

import (
	"context"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	appdb "github.com/devpad-org/devpad/internal/database"
)

func openMigratedTestDB(t *testing.T) *appdb.DB {
	t.Helper()
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	return db
}

func TestAgentDefaultsRepositoryUpsertsAndDeletesPerUser(t *testing.T) {
	db := openMigratedTestDB(t)
	alice := insertAgentTestUser(t, db, "alice", "alice@example.com")
	bob := insertAgentTestUser(t, db, "bob", "bob@example.com")
	repo := NewAgentDefaultsRepository(db.Conn())
	ctx := context.Background()

	if err := repo.SetAgentDefaults(ctx, alice, domain.AgentDefaults{AgentID: "review", Model: "a", Effort: "low"}); err != nil {
		t.Fatalf("set review defaults: %v", err)
	}
	if err := repo.SetAgentDefaults(ctx, alice, domain.AgentDefaults{AgentID: "default", Model: "b"}); err != nil {
		t.Fatalf("set coding defaults: %v", err)
	}
	if err := repo.SetAgentDefaults(ctx, alice, domain.AgentDefaults{AgentID: "review", Model: "c"}); err != nil {
		t.Fatalf("replace review defaults: %v", err)
	}
	if err := repo.SetAgentDefaults(ctx, bob, domain.AgentDefaults{AgentID: "review", Model: "z"}); err != nil {
		t.Fatalf("set bob's review defaults: %v", err)
	}

	got, err := repo.ListAgentDefaults(ctx, alice)
	if err != nil {
		t.Fatalf("list defaults: %v", err)
	}
	want := []domain.AgentDefaults{{AgentID: "default", Model: "b"}, {AgentID: "review", Model: "c"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("expected %+v, got %+v", want, got)
	}

	if err := repo.DeleteAgentDefaults(ctx, alice, "review"); err != nil {
		t.Fatalf("delete defaults: %v", err)
	}
	got, err = repo.ListAgentDefaults(ctx, alice)
	if err != nil {
		t.Fatalf("list defaults after delete: %v", err)
	}
	if len(got) != 1 || got[0].AgentID != "default" {
		t.Fatalf("expected only coding defaults left, got %+v", got)
	}
	got, err = repo.ListAgentDefaults(ctx, bob)
	if err != nil || len(got) != 1 || got[0].Model != "z" {
		t.Fatalf("expected bob's own review default untouched, got %+v %v", got, err)
	}
}
