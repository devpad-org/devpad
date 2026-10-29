package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
	appdb "github.com/devpad-org/devpad/internal/database"
)

func openAttachmentTestDB(t *testing.T) *appdb.DB {
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

func insertOtherTestUser(t *testing.T, db *appdb.DB) int64 {
	t.Helper()
	result, err := db.Conn().Exec(`INSERT INTO users (username, email, password) VALUES ('other', 'other@example.com', 'hashed')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("get user id: %v", err)
	}
	return id
}

func createTestAttachment(t *testing.T, repo *AttachmentRepository, userID int64) *domain.Attachment {
	t.Helper()
	a := &domain.Attachment{UserID: userID, MediaType: "image/png", Width: 4, Height: 3, Data: []byte("png-bytes")}
	if err := repo.CreateAttachment(context.Background(), a); err != nil {
		t.Fatalf("create attachment: %v", err)
	}
	return a
}

func TestAttachmentRepository_ScopesAttachmentsToTheirOwner(t *testing.T) {
	db := openAttachmentTestDB(t)
	ctx := context.Background()
	repo := NewAttachmentRepository(db.Conn())
	owner := insertAgentRunTestUser(t, db)
	other := insertOtherTestUser(t, db)
	a := createTestAttachment(t, repo, owner)

	got, err := repo.GetAttachment(ctx, owner, a.ID)
	if err != nil {
		t.Fatalf("get own attachment: %v", err)
	}
	if got.MediaType != "image/png" || got.Width != 4 || got.Height != 3 || string(got.Data) != "png-bytes" {
		t.Fatalf("unexpected attachment: %+v", got)
	}
	if _, err := repo.GetAttachment(ctx, other, a.ID); !errors.Is(err, domain.ErrAttachmentNotFound) {
		t.Fatalf("other user's get err = %v, want ErrAttachmentNotFound", err)
	}
	found, err := repo.GetAttachments(ctx, other, []int64{a.ID})
	if err != nil {
		t.Fatalf("get attachments: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("other user found %d attachments, want 0", len(found))
	}
}

func TestConversationRepository_SavesImagePartsAndLinksTheirAttachments(t *testing.T) {
	db := openAttachmentTestDB(t)
	ctx := context.Background()
	attachments := NewAttachmentRepository(db.Conn())
	conversations := NewConversationRepository(db.Conn())
	userID := insertAgentRunTestUser(t, db)
	workspaceID := insertAgentRunTestWorkspace(t, db, userID)
	other := insertOtherTestUser(t, db)

	conv := &domain.Conversation{UserID: userID, WorkspaceID: workspaceID}
	if err := conversations.CreateConversation(ctx, conv); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	uploaded := createTestAttachment(t, attachments, userID)
	fromTool := createTestAttachment(t, attachments, userID)
	foreign := createTestAttachment(t, attachments, other)

	turns := []domain.Turn{
		{Role: domain.RoleUser, Parts: []domain.Part{
			domain.NewImagePart(uploaded.ImagePart()),
			{Kind: domain.PartText, Text: "what is this?"},
			domain.NewImagePart(foreign.ImagePart()),
		}},
		domain.NewToolResultTurnFromPart(domain.ToolResultPart{
			ToolCallID: "call-1", Name: "read_file", Content: "Image a.png", Images: []domain.ImagePart{fromTool.ImagePart()},
		}),
	}
	if err := conversations.SaveTurns(ctx, conv.ID, turns); err != nil {
		t.Fatalf("save turns: %v", err)
	}

	got, err := conversations.GetTurns(ctx, conv.ID, userID)
	if err != nil {
		t.Fatalf("get turns: %v", err)
	}
	if len(got) != 2 || len(got[0].Parts) != 3 {
		t.Fatalf("unexpected turns: %+v", got)
	}
	image := got[0].Parts[0].Image
	if got[0].Parts[0].Kind != domain.PartImage || image == nil || image.AttachmentID != uploaded.ID ||
		image.MediaType != "image/png" || image.Width != 4 || image.Height != 3 {
		t.Fatalf("unexpected image part: %+v", got[0].Parts[0])
	}
	if len(image.Data) != 0 {
		t.Fatal("expected saved turns to carry no image data")
	}
	result := got[1].ToolResult()
	if result == nil || len(result.Images) != 1 || result.Images[0].AttachmentID != fromTool.ID {
		t.Fatalf("unexpected tool result: %+v", result)
	}

	linked, err := attachments.GetAttachments(ctx, userID, []int64{uploaded.ID, fromTool.ID})
	if err != nil {
		t.Fatalf("get attachments: %v", err)
	}
	for _, id := range []int64{uploaded.ID, fromTool.ID} {
		if linked[id].ConversationID != conv.ID {
			t.Fatalf("attachment %d conversation = %d, want %d", id, linked[id].ConversationID, conv.ID)
		}
	}
	stillForeign, err := attachments.GetAttachment(ctx, other, foreign.ID)
	if err != nil {
		t.Fatalf("get foreign attachment: %v", err)
	}
	if stillForeign.ConversationID != 0 {
		t.Fatal("expected another user's attachment to stay out of the conversation")
	}
}

func TestAttachmentRepository_DeletesOnlyOldUnreferencedAttachments(t *testing.T) {
	db := openAttachmentTestDB(t)
	ctx := context.Background()
	repo := NewAttachmentRepository(db.Conn())
	conversations := NewConversationRepository(db.Conn())
	userID := insertAgentRunTestUser(t, db)
	workspaceID := insertAgentRunTestWorkspace(t, db, userID)
	conv := &domain.Conversation{UserID: userID, WorkspaceID: workspaceID}
	if err := conversations.CreateConversation(ctx, conv); err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	unreferenced := createTestAttachment(t, repo, userID)
	referenced := &domain.Attachment{UserID: userID, ConversationID: conv.ID, MediaType: "image/png", Width: 1, Height: 1, Data: []byte("x")}
	if err := repo.CreateAttachment(ctx, referenced); err != nil {
		t.Fatalf("create referenced attachment: %v", err)
	}

	if err := repo.DeleteUnreferencedAttachments(ctx, userID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("sweep recent: %v", err)
	}
	if _, err := repo.GetAttachment(ctx, userID, unreferenced.ID); err != nil {
		t.Fatalf("expected a recent upload to survive the sweep: %v", err)
	}

	if err := repo.DeleteUnreferencedAttachments(ctx, userID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("sweep old: %v", err)
	}
	if _, err := repo.GetAttachment(ctx, userID, unreferenced.ID); !errors.Is(err, domain.ErrAttachmentNotFound) {
		t.Fatalf("unreferenced attachment err = %v, want ErrAttachmentNotFound", err)
	}
	if _, err := repo.GetAttachment(ctx, userID, referenced.ID); err != nil {
		t.Fatalf("expected the conversation's attachment to survive: %v", err)
	}
}

func TestAttachmentRepository_GetAttachmentInfoLeavesOutTheData(t *testing.T) {
	db := openAttachmentTestDB(t)
	ctx := context.Background()
	repo := NewAttachmentRepository(db.Conn())
	userID := insertAgentRunTestUser(t, db)
	a := createTestAttachment(t, repo, userID)
	if a.Size != len("png-bytes") {
		t.Fatalf("created size = %d, want %d", a.Size, len("png-bytes"))
	}

	info, err := repo.GetAttachmentInfo(ctx, userID, []int64{a.ID})
	if err != nil {
		t.Fatalf("get attachment info: %v", err)
	}
	got := info[a.ID]
	if got.Size != len("png-bytes") || got.MediaType != "image/png" || got.Data != nil {
		t.Fatalf("unexpected info: %+v", got)
	}
}

func TestAttachmentRepository_FindToolImageMatchesTheRunAndConversation(t *testing.T) {
	db := openAttachmentTestDB(t)
	ctx := context.Background()
	repo := NewAttachmentRepository(db.Conn())
	conversations := NewConversationRepository(db.Conn())
	userID := insertAgentRunTestUser(t, db)
	workspaceID := insertAgentRunTestWorkspace(t, db, userID)
	conv := &domain.Conversation{UserID: userID, WorkspaceID: workspaceID}
	if err := conversations.CreateConversation(ctx, conv); err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	stored := &domain.Attachment{UserID: userID, ConversationID: conv.ID, MediaType: "image/png", Width: 1, Height: 1, Data: []byte("x"), SourceSHA256: "abc"}
	if err := repo.CreateAttachment(ctx, stored); err != nil {
		t.Fatalf("create attachment: %v", err)
	}

	found, err := repo.FindToolImage(ctx, userID, conv.ID, 0, "abc")
	if err != nil {
		t.Fatalf("find tool image: %v", err)
	}
	if found.ID != stored.ID || string(found.Data) != "x" || found.Size != 1 {
		t.Fatalf("unexpected tool image: %+v", found)
	}
	for _, tt := range []struct {
		name                 string
		userID, conversation int64
		hash                 string
	}{
		{"another file", userID, conv.ID, "def"},
		{"outside a conversation", userID, 0, "abc"},
		{"another user", insertOtherTestUser(t, db), conv.ID, "abc"},
	} {
		if _, err := repo.FindToolImage(ctx, tt.userID, tt.conversation, 0, tt.hash); !errors.Is(err, domain.ErrAttachmentNotFound) {
			t.Fatalf("%s: err = %v, want ErrAttachmentNotFound", tt.name, err)
		}
	}
}
