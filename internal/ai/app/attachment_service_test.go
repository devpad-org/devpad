package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"reflect"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

type fakeAttachmentRepo struct {
	attachments map[int64]domain.Attachment
	loads       int
	// loaded is every attachment whose data was loaded.
	loaded []int64
}

func (r *fakeAttachmentRepo) CreateAttachment(_ context.Context, a *domain.Attachment) error {
	a.ID = int64(len(r.attachments) + 1)
	a.Size = len(a.Data)
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
	r.loaded = append(r.loaded, ids...)
	found := make(map[int64]domain.Attachment)
	for _, id := range ids {
		if a, ok := r.attachments[id]; ok && a.UserID == userID {
			found[id] = a
		}
	}
	return found, nil
}

func (r *fakeAttachmentRepo) GetAttachmentInfo(_ context.Context, userID int64, ids []int64) (map[int64]domain.Attachment, error) {
	found := make(map[int64]domain.Attachment)
	for _, id := range ids {
		if a, ok := r.attachments[id]; ok && a.UserID == userID {
			a.Size, a.Data = len(a.Data), nil
			found[id] = a
		}
	}
	return found, nil
}

func (r *fakeAttachmentRepo) FindToolImage(_ context.Context, userID, conversationID, runID int64, sourceSHA256 string) (*domain.Attachment, error) {
	for id := int64(1); id <= int64(len(r.attachments)); id++ {
		a, ok := r.attachments[id]
		if ok && a.UserID == userID && a.ConversationID == conversationID && a.RunID == runID && a.SourceSHA256 == sourceSHA256 {
			return &a, nil
		}
	}
	return nil, domain.ErrAttachmentNotFound
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

func TestAttachmentService_HydrateLoadsOnlyTheImagesARequestSends(t *testing.T) {
	repo := &fakeAttachmentRepo{attachments: map[int64]domain.Attachment{}}
	var turns []domain.Turn
	for id := int64(1); id <= 30; id++ {
		repo.attachments[id] = domain.Attachment{ID: id, UserID: 7, MediaType: "image/png", Width: 2, Height: 2, Data: []byte(fmt.Sprintf("image-%d", id))}
		turns = append(turns, domain.Turn{Role: domain.RoleUser, Parts: []domain.Part{domain.NewImagePart(domain.ImagePart{AttachmentID: id})}})
	}

	got, err := NewAttachmentService(repo).Hydrate(context.Background(), 7, turns)
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	// 30 images drop the oldest step of 10 and send the latest 20.
	if fmt.Sprint(repo.loaded) != fmt.Sprint([]int64{11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30}) {
		t.Fatalf("loaded the data of %v, want 11 to 30", repo.loaded)
	}
	first, last := got[0].Parts[0].Image, got[29].Parts[0].Image
	if first.Size == 0 || first.MediaType != "image/png" || len(first.Data) != 0 {
		t.Fatalf("an image no request sends should keep its size and no data: %+v", first)
	}
	if string(last.Data) != "image-30" {
		t.Fatalf("unexpected newest image: %+v", last)
	}
}

func TestAttachmentService_StoreToolImageReusesTheRunsCopyOfAFile(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	repo := &fakeAttachmentRepo{attachments: map[int64]domain.Attachment{}}
	service := NewAttachmentService(repo)
	ctx := context.Background()

	first, err := service.StoreToolImage(ctx, 7, 3, 4, buf.Bytes())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	again, err := service.StoreToolImage(ctx, 7, 3, 4, buf.Bytes())
	if err != nil {
		t.Fatalf("store again: %v", err)
	}
	if again.AttachmentID != first.AttachmentID || len(again.Data) == 0 || again.Size != first.Size {
		t.Fatalf("second read = %+v, want the first copy %+v", again, first)
	}
	otherRun, err := service.StoreToolImage(ctx, 7, 3, 5, buf.Bytes())
	if err != nil {
		t.Fatalf("store in another run: %v", err)
	}
	if otherRun.AttachmentID == first.AttachmentID || len(repo.attachments) != 2 {
		t.Fatalf("another run should store its own copy, got %d attachments", len(repo.attachments))
	}
}
