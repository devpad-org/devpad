package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	appdb "github.com/devpad-org/devpad/internal/database"
)

func TestAgentRepositoryListsWorkspaceAndGlobalAgents(t *testing.T) {
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	userID := insertAgentRunTestUser(t, db)
	workspaceID := insertAgentRunTestWorkspace(t, db, userID)
	otherWorkspaceID := insertAgentRunTestWorkspace(t, db, userID)
	repo := NewAgentRepository(db.Conn())
	ctx := context.Background()

	workspaceAgent := &domain.Agent{UserID: userID, WorkspaceID: workspaceID, Name: "Code Review", Purpose: "Review changes", Instructions: "Review the diff."}
	if err := repo.CreateAgent(ctx, workspaceAgent); err != nil {
		t.Fatalf("create workspace agent: %v", err)
	}
	globalAgent := &domain.Agent{UserID: userID, IsGlobal: true, Name: "UI Design", Purpose: "Design", Instructions: "Improve UI."}
	if err := repo.CreateAgent(ctx, globalAgent); err != nil {
		t.Fatalf("create global agent: %v", err)
	}
	otherAgent := &domain.Agent{UserID: userID, WorkspaceID: otherWorkspaceID, Name: "Other", Instructions: "Other."}
	if err := repo.CreateAgent(ctx, otherAgent); err != nil {
		t.Fatalf("create other agent: %v", err)
	}

	agents, err := repo.ListAgents(ctx, userID, workspaceID)
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected workspace and global agents, got %+v", agents)
	}
	for _, agent := range agents {
		if agent.ID == otherAgent.ID {
			t.Fatalf("did not expect agent from another workspace: %+v", agents)
		}
	}
}

func TestAgentRepositoryRejectsWorkspaceOwnedByAnotherUser(t *testing.T) {
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	ownerID := insertAgentRunTestUser(t, db)
	workspaceID := insertAgentRunTestWorkspace(t, db, ownerID)
	otherUserID := insertAgentTestUser(t, db, "other-agent-user", "other-agent-user@example.com")
	repo := NewAgentRepository(db.Conn())

	err = repo.CreateAgent(context.Background(), &domain.Agent{
		UserID:       otherUserID,
		WorkspaceID:  workspaceID,
		Name:         "Cross tenant",
		Instructions: "Should not be created.",
	})
	if !errors.Is(err, domain.ErrAgentNotFound) {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}

func insertAgentTestUser(t *testing.T, db *appdb.DB, username, email string) int64 {
	t.Helper()
	result, err := db.Conn().Exec(
		`INSERT INTO users (username, email, password) VALUES (?, ?, ?)`,
		username,
		email,
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
