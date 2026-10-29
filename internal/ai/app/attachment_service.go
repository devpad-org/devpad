package app

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/devpad-org/devpad/internal/ai/attachment"
	"github.com/devpad-org/devpad/internal/ai/domain"
)

// unreferencedAttachmentTTL is how long an upload is kept before a saved
// conversation refers to it.
const unreferencedAttachmentTTL = 24 * time.Hour

// AttachmentRepository stores image attachments.
type AttachmentRepository interface {
	CreateAttachment(ctx context.Context, a *domain.Attachment) error
	GetAttachment(ctx context.Context, userID, id int64) (*domain.Attachment, error)
	GetAttachments(ctx context.Context, userID int64, ids []int64) (map[int64]domain.Attachment, error)
	DeleteUnreferencedAttachments(ctx context.Context, userID int64, before time.Time) error
}

// ImageLoader fills in the image data that turns refer to.
type ImageLoader interface {
	// Hydrate returns the turns with the data of every image they refer to
	// loaded, or domain.ErrAttachmentNotFound when one is not the user's.
	Hydrate(ctx context.Context, userID int64, turns []domain.Turn) ([]domain.Turn, error)
}

// AttachmentService stores the images people attach to messages and tools
// return, and loads them for runs.
type AttachmentService interface {
	ImageLoader
	// Upload normalizes and stores an image the user attached.
	Upload(ctx context.Context, userID int64, data []byte) (*domain.Attachment, error)
	// StoreToolImage normalizes and stores an image a tool returned in a run.
	StoreToolImage(ctx context.Context, userID, conversationID, runID int64, data []byte) (domain.ImagePart, error)
	// GetAttachment returns one of the user's attachments.
	GetAttachment(ctx context.Context, userID, id int64) (*domain.Attachment, error)
}

type attachmentService struct {
	repo AttachmentRepository
	now  func() time.Time
}

// NewAttachmentService creates an AttachmentService backed by the given repository.
func NewAttachmentService(repo AttachmentRepository) AttachmentService {
	return &attachmentService{repo: repo, now: time.Now}
}

func (s *attachmentService) Upload(ctx context.Context, userID int64, data []byte) (*domain.Attachment, error) {
	image, err := attachment.Normalize(data)
	if err != nil {
		return nil, err
	}
	// Uploads removed from the composer, or never sent, are left behind.
	if err := s.repo.DeleteUnreferencedAttachments(ctx, userID, s.now().Add(-unreferencedAttachmentTTL)); err != nil {
		log.Printf("sweeping unreferenced attachments for user %d: %v", userID, err)
	}
	a := &domain.Attachment{
		UserID:    userID,
		MediaType: image.MediaType,
		Width:     image.Width,
		Height:    image.Height,
		Data:      image.Data,
	}
	if err := s.repo.CreateAttachment(ctx, a); err != nil {
		return nil, fmt.Errorf("storing attachment: %w", err)
	}
	return a, nil
}

func (s *attachmentService) StoreToolImage(ctx context.Context, userID, conversationID, runID int64, data []byte) (domain.ImagePart, error) {
	image, err := attachment.Normalize(data)
	if err != nil {
		return domain.ImagePart{}, err
	}
	a := &domain.Attachment{
		UserID:         userID,
		ConversationID: conversationID,
		RunID:          runID,
		MediaType:      image.MediaType,
		Width:          image.Width,
		Height:         image.Height,
		Data:           image.Data,
	}
	if err := s.repo.CreateAttachment(ctx, a); err != nil {
		return domain.ImagePart{}, fmt.Errorf("storing tool image: %w", err)
	}
	return a.ImagePart(), nil
}

func (s *attachmentService) GetAttachment(ctx context.Context, userID, id int64) (*domain.Attachment, error) {
	return s.repo.GetAttachment(ctx, userID, id)
}

func (s *attachmentService) Hydrate(ctx context.Context, userID int64, turns []domain.Turn) ([]domain.Turn, error) {
	var missing []int64
	for _, turn := range turns {
		for _, part := range turn.Parts {
			switch {
			case part.Kind == domain.PartImage:
				if part.Image == nil || part.Image.AttachmentID <= 0 {
					return nil, fmt.Errorf("%w: image part has no attachment", domain.ErrAttachmentNotFound)
				}
				if len(part.Image.Data) == 0 {
					missing = append(missing, part.Image.AttachmentID)
				}
			case part.Kind == domain.PartToolResult && part.ToolResult != nil:
				for _, image := range part.ToolResult.Images {
					if len(image.Data) == 0 {
						missing = append(missing, image.AttachmentID)
					}
				}
			}
		}
	}
	if len(missing) == 0 {
		return turns, nil
	}

	loaded, err := s.repo.GetAttachments(ctx, userID, missing)
	if err != nil {
		return nil, fmt.Errorf("loading attachments: %w", err)
	}
	// The stored attachment, not the client's copy of its reference, says
	// what the image is.
	resolve := func(image domain.ImagePart) (domain.ImagePart, error) {
		if len(image.Data) > 0 {
			return image, nil
		}
		a, ok := loaded[image.AttachmentID]
		if !ok {
			return domain.ImagePart{}, fmt.Errorf("%w: %d", domain.ErrAttachmentNotFound, image.AttachmentID)
		}
		return a.ImagePart(), nil
	}

	hydrated := make([]domain.Turn, len(turns))
	for i, turn := range turns {
		hydrated[i] = domain.Turn{Role: turn.Role, Parts: make([]domain.Part, len(turn.Parts))}
		for j, part := range turn.Parts {
			switch {
			case part.Kind == domain.PartImage:
				image, err := resolve(*part.Image)
				if err != nil {
					return nil, err
				}
				part.Image = &image
			case part.Kind == domain.PartToolResult && part.ToolResult != nil && len(part.ToolResult.Images) > 0:
				result := *part.ToolResult
				result.Images = make([]domain.ImagePart, len(part.ToolResult.Images))
				for k, image := range part.ToolResult.Images {
					if result.Images[k], err = resolve(image); err != nil {
						return nil, err
					}
				}
				part.ToolResult = &result
			}
			hydrated[i].Parts[j] = part
		}
	}
	return hydrated, nil
}
