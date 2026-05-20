package storage

import (
	"context"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	appdb "github.com/devpad-org/devpad/internal/database"
)

func TestConversationRepository_PersistsImageParts(t *testing.T) {
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

	conversation := &domain.Conversation{
		UserID:      userID,
		WorkspaceID: workspaceID,
		Title:       "Images",
		Model:       "gpt-5.4",
	}
	if err := repo.CreateConversation(ctx, conversation); err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	turns := []domain.Turn{{
		Role: domain.RoleUser,
		Parts: []domain.Part{
			{Kind: domain.PartText, Text: "What is in this screenshot?"},
			{Kind: domain.PartImage, Image: &domain.ImagePart{MIMEType: "image/png", Data: "aGVsbG8="}},
		},
	}}
	if err := repo.SaveTurns(ctx, conversation.ID, turns); err != nil {
		t.Fatalf("save turns: %v", err)
	}

	stored, err := repo.GetTurns(ctx, conversation.ID, userID)
	if err != nil {
		t.Fatalf("get turns: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(stored))
	}
	if stored[0].Text() != "What is in this screenshot?" {
		t.Fatalf("unexpected text: %q", stored[0].Text())
	}
	images := stored[0].Images()
	if len(images) != 1 || images[0].MIMEType != "image/png" || images[0].Data != "aGVsbG8=" {
		t.Fatalf("unexpected images: %+v", images)
	}
}
