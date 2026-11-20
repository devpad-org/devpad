package storage

import (
	"context"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	appdb "github.com/devpad-org/devpad/internal/database"
)

func TestConversationRepository_AppendAndReplaceTurns(t *testing.T) {
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
	repo := NewConversationRepository(db.Conn())

	conv := &domain.Conversation{UserID: userID, WorkspaceID: workspaceID, Model: "gpt-5.4"}
	if err := repo.CreateConversation(ctx, conv); err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	call := domain.ToolCall{ID: "call-1", Type: "function", Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"a.go"}`}}
	if err := repo.AppendTurns(ctx, conv.ID, []domain.Turn{domain.NewTextTurn(domain.RoleUser, "read a.go")}); err != nil {
		t.Fatalf("append first turn: %v", err)
	}
	if err := repo.AppendTurns(ctx, conv.ID, []domain.Turn{
		{Role: domain.RoleAssistant, Parts: []domain.Part{
			{Kind: domain.PartText, Text: "Reading it."},
			{Kind: domain.PartToolCall, ToolCall: &call},
		}},
		domain.NewToolResultTurn("call-1", "read_file", "package a", false),
	}); err != nil {
		t.Fatalf("append step: %v", err)
	}

	turns, err := repo.GetTurns(ctx, conv.ID, userID)
	if err != nil {
		t.Fatalf("get turns: %v", err)
	}
	if len(turns) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(turns))
	}
	if turns[0].Text() != "read a.go" || turns[1].Text() != "Reading it." {
		t.Fatalf("turns out of order: %+v", turns)
	}
	if calls := turns[1].ToolCalls(); len(calls) != 1 || calls[0].ID != "call-1" {
		t.Fatalf("expected the tool call to round-trip, got %+v", calls)
	}
	if result := turns[2].ToolResult(); result == nil || result.ToolCallID != "call-1" {
		t.Fatalf("expected the tool result to round-trip, got %+v", turns[2])
	}

	if err := repo.ReplaceTurns(ctx, conv.ID, []domain.Turn{domain.NewTextTurn(domain.RoleUser, "start over")}); err != nil {
		t.Fatalf("replace turns: %v", err)
	}
	if err := repo.AppendTurns(ctx, conv.ID, []domain.Turn{domain.NewTextTurn(domain.RoleAssistant, "ok")}); err != nil {
		t.Fatalf("append after replace: %v", err)
	}
	turns, err = repo.GetTurns(ctx, conv.ID, userID)
	if err != nil {
		t.Fatalf("get turns after replace: %v", err)
	}
	if len(turns) != 2 || turns[0].Text() != "start over" || turns[1].Text() != "ok" {
		t.Fatalf("unexpected turns after replace: %+v", turns)
	}
}
