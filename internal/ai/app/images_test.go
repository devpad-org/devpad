package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func loadedImage(id int64) domain.ImagePart {
	return domain.ImagePart{AttachmentID: id, MediaType: "image/png", Width: 10, Height: 10, Data: []byte(fmt.Sprintf("image-%d", id))}
}

func imageTurn(ids ...int64) domain.Turn {
	turn := domain.Turn{Role: domain.RoleUser}
	for _, id := range ids {
		turn.Parts = append(turn.Parts, domain.NewImagePart(loadedImage(id)))
	}
	return turn
}

func sentImageIDs(turns []domain.Turn) []int64 {
	var ids []int64
	for _, turn := range turns {
		for _, part := range turn.Parts {
			if part.Kind == domain.PartImage {
				ids = append(ids, part.Image.AttachmentID)
			}
			if part.Kind == domain.PartToolResult {
				for _, image := range part.ToolResult.Images {
					ids = append(ids, image.AttachmentID)
				}
			}
		}
	}
	return ids
}

func TestPrepareImages_ReplacesImagesForModelsWithoutVision(t *testing.T) {
	turns := []domain.Turn{
		imageTurn(1),
		domain.NewToolResultTurnFromPart(domain.ToolResultPart{ToolCallID: "c", Name: "read_file", Content: "Image a.png", Images: []domain.ImagePart{loadedImage(2)}}),
	}

	got := prepareImages(domain.Model{ID: "text-only", Name: "Text Only"}, turns)

	if ids := sentImageIDs(got); len(ids) != 0 {
		t.Fatalf("sent images %v to a model without vision", ids)
	}
	if got[0].Parts[0].Kind != domain.PartText || !strings.Contains(got[0].Parts[0].Text, "Text Only can't view images") {
		t.Fatalf("unexpected placeholder: %+v", got[0].Parts[0])
	}
	if content := got[1].ToolResult().Content; !strings.HasPrefix(content, "Image a.png\n") || !strings.Contains(content, "can't view images") {
		t.Fatalf("unexpected tool result content: %q", content)
	}
	if turns[0].Parts[0].Kind != domain.PartImage || len(turns[1].ToolResult().Images) != 1 {
		t.Fatal("expected the input turns to be left unchanged")
	}
}

func TestPrepareImages_KeepsOnlyTheLatestImages(t *testing.T) {
	var turns []domain.Turn
	for id := int64(1); id <= maxImagesPerRequest+3; id++ {
		turns = append(turns, imageTurn(id))
	}

	got := prepareImages(domain.Model{Vision: true}, turns)

	ids := sentImageIDs(got)
	if len(ids) != maxImagesPerRequest || ids[0] != 4 || ids[len(ids)-1] != maxImagesPerRequest+3 {
		t.Fatalf("sent images %v, want the latest %d", ids, maxImagesPerRequest)
	}
	if !strings.Contains(got[0].Text(), "Earlier image not shown") {
		t.Fatalf("unexpected placeholder: %q", got[0].Text())
	}
}

func TestPrepareImages_SharesTurnsThatNeedNoChange(t *testing.T) {
	turns := []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hi"), imageTurn(1)}
	got := prepareImages(domain.Model{Vision: true}, turns)
	if &got[0] != &turns[0] {
		t.Fatal("expected unchanged turns to be returned as they are")
	}
}

func TestPrepareImages_ReplacesImagesWithoutData(t *testing.T) {
	turn := domain.Turn{Role: domain.RoleUser, Parts: []domain.Part{domain.NewImagePart(domain.ImagePart{AttachmentID: 9})}}
	got := prepareImages(domain.Model{Vision: true}, []domain.Turn{turn})
	if got[0].Parts[0].Kind != domain.PartText || !strings.Contains(got[0].Parts[0].Text, "no longer available") {
		t.Fatalf("unexpected part: %+v", got[0].Parts[0])
	}
}

type fakeAttachmentRepo struct {
	attachments map[int64]domain.Attachment
	loads       int
}

func (r *fakeAttachmentRepo) CreateAttachment(_ context.Context, a *domain.Attachment) error {
	a.ID = int64(len(r.attachments) + 1)
	r.attachments[a.ID] = *a
	return nil
}

func (r *fakeAttachmentRepo) GetAttachment(_ context.Context, userID, id int64) (*domain.Attachment, error) {
	a, ok := r.attachments[id]
	if !ok || a.UserID != userID {
		return nil, domain.ErrAttachmentNotFound
	}
	return &a, nil
}

func (r *fakeAttachmentRepo) GetAttachments(_ context.Context, userID int64, ids []int64) (map[int64]domain.Attachment, error) {
	r.loads++
	found := make(map[int64]domain.Attachment)
	for _, id := range ids {
		if a, ok := r.attachments[id]; ok && a.UserID == userID {
			found[id] = a
		}
	}
	return found, nil
}

func (r *fakeAttachmentRepo) DeleteUnreferencedAttachments(context.Context, int64, time.Time) error {
	return nil
}

func TestAttachmentService_HydrateLoadsTheUsersImages(t *testing.T) {
	repo := &fakeAttachmentRepo{attachments: map[int64]domain.Attachment{
		1: {ID: 1, UserID: 7, MediaType: "image/jpeg", Width: 3, Height: 2, Data: []byte("one")},
		2: {ID: 2, UserID: 7, MediaType: "image/png", Width: 5, Height: 4, Data: []byte("two")},
	}}
	service := NewAttachmentService(repo)
	// The client's copy of the reference is not trusted for type or size.
	turns := []domain.Turn{
		{Role: domain.RoleUser, Parts: []domain.Part{domain.NewImagePart(domain.ImagePart{AttachmentID: 1, MediaType: "image/svg+xml"})}},
		domain.NewToolResultTurnFromPart(domain.ToolResultPart{ToolCallID: "c", Images: []domain.ImagePart{{AttachmentID: 2}}}),
	}

	got, err := service.Hydrate(context.Background(), 7, turns)
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	image := got[0].Parts[0].Image
	if string(image.Data) != "one" || image.MediaType != "image/jpeg" || image.Width != 3 {
		t.Fatalf("unexpected image: %+v", image)
	}
	if string(got[1].ToolResult().Images[0].Data) != "two" {
		t.Fatalf("unexpected tool image: %+v", got[1].ToolResult().Images[0])
	}
	if len(turns[0].Parts[0].Image.Data) != 0 {
		t.Fatal("expected the input turns to be left unchanged")
	}

	again, err := service.Hydrate(context.Background(), 7, got)
	if err != nil {
		t.Fatalf("hydrate again: %v", err)
	}
	if repo.loads != 1 || &again[0] != &got[0] {
		t.Fatal("expected loaded turns to be returned without loading again")
	}
}

func TestAttachmentService_HydrateRefusesOtherUsersImages(t *testing.T) {
	repo := &fakeAttachmentRepo{attachments: map[int64]domain.Attachment{
		1: {ID: 1, UserID: 8, MediaType: "image/png", Data: []byte("theirs")},
	}}
	service := NewAttachmentService(repo)
	turns := []domain.Turn{{Role: domain.RoleUser, Parts: []domain.Part{domain.NewImagePart(domain.ImagePart{AttachmentID: 1})}}}

	if _, err := service.Hydrate(context.Background(), 7, turns); !errors.Is(err, domain.ErrAttachmentNotFound) {
		t.Fatalf("err = %v, want ErrAttachmentNotFound", err)
	}
}
