package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

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

func TestAttachmentService_HydrateLeavesImagesItCannotLoadWithoutData(t *testing.T) {
	repo := &fakeAttachmentRepo{attachments: map[int64]domain.Attachment{
		1: {ID: 1, UserID: 8, MediaType: "image/png", Width: 4, Height: 4, Data: []byte("theirs")},
	}}
	service := NewAttachmentService(repo)
	// Another user's image, and one that is gone. The client's type and
	// size for them are not kept either.
	turns := []domain.Turn{{Role: domain.RoleUser, Parts: []domain.Part{
		domain.NewImagePart(domain.ImagePart{AttachmentID: 1, MediaType: "image/png", Width: 4, Height: 4}),
		domain.NewImagePart(domain.ImagePart{AttachmentID: 2, MediaType: "image/png", Width: 9000, Height: 9000}),
	}}}

	got, err := service.Hydrate(context.Background(), 7, turns)
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	for i, part := range got[0].Parts {
		if !reflect.DeepEqual(*part.Image, domain.ImagePart{AttachmentID: int64(i + 1)}) {
			t.Fatalf("image %d = %+v, want only its reference", i, part.Image)
		}
	}
}

func TestAttachmentService_HydrateRefusesImagePartsWithoutAReference(t *testing.T) {
	service := NewAttachmentService(&fakeAttachmentRepo{attachments: map[int64]domain.Attachment{}})
	turns := []domain.Turn{{Role: domain.RoleUser, Parts: []domain.Part{{Kind: domain.PartImage}}}}

	if _, err := service.Hydrate(context.Background(), 7, turns); !errors.Is(err, domain.ErrAttachmentNotFound) {
		t.Fatalf("err = %v, want ErrAttachmentNotFound", err)
	}
}
