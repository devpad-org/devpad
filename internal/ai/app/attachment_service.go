package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	// GetAttachmentInfo is GetAttachments without the images' data.
	GetAttachmentInfo(ctx context.Context, userID int64, ids []int64) (map[int64]domain.Attachment, error)
	FindToolImage(ctx context.Context, userID, conversationID, runID int64, sourceSHA256 string) (*domain.Attachment, error)
	DeleteUnreferencedAttachments(ctx context.Context, userID int64, before time.Time) error
}

// ImageLoader fills in the image data that turns refer to.
type ImageLoader interface {
	// Hydrate returns the turns with the size of every image of the user's
	// they refer to, and the data of those a model request will carry,
	// loaded. An image that is gone, or is not the user's, is left with
	// neither, which the model is told is no longer available: one lost
	// image must not stop the whole conversation going on. Only an image
	// part with no reference at all is refused, with
	// domain.ErrAttachmentNotFound.
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
	image, err := attachment.Normalize(ctx, data)
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
	// A run that reads the same file again refers to the copy it stored.
	sum := sha256.Sum256(data)
	sourceSHA256 := hex.EncodeToString(sum[:])
	if conversationID > 0 || runID > 0 {
		existing, err := s.repo.FindToolImage(ctx, userID, conversationID, runID, sourceSHA256)
		if err == nil {
			return existing.ImagePart(), nil
		}
		if !errors.Is(err, domain.ErrAttachmentNotFound) {
			return domain.ImagePart{}, fmt.Errorf("looking up tool image: %w", err)
		}
	}

	image, err := attachment.Normalize(ctx, data)
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
		SourceSHA256:   sourceSHA256,
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
	var unsized []int64
	for _, turn := range turns {
		for _, part := range turn.Parts {
			if part.Kind == domain.PartImage && (part.Image == nil || part.Image.AttachmentID <= 0) {
				return nil, fmt.Errorf("%w: image part has no attachment", domain.ErrAttachmentNotFound)
			}
		}
	}
	domain.EachImage(turns, func(image domain.ImagePart) {
		if image.Size == 0 && len(image.Data) == 0 {
			unsized = append(unsized, image.AttachmentID)
		}
	})

	// The stored attachments say which images are there, what they are and
	// how much of a request each takes. Without one, nothing the client
	// said about the image is kept.
	if len(unsized) > 0 {
		info, err := s.repo.GetAttachmentInfo(ctx, userID, unsized)
		if err != nil {
			return nil, fmt.Errorf("loading attachments: %w", err)
		}
		turns = mapImages(turns, func(image domain.ImagePart) domain.ImagePart {
			if image.Size > 0 || len(image.Data) > 0 {
				return image
			}
			if a, ok := info[image.AttachmentID]; ok {
				return a.ImagePart()
			}
			return domain.ImagePart{AttachmentID: image.AttachmentID}
		})
	}

	// Load the data of only the images a request will carry. The rest stay
	// in the database: a long conversation can refer to far more than any
	// request sends.
	var unloaded []int64
	for _, image := range attachment.Sent(turns) {
		if len(image.Data) == 0 {
			unloaded = append(unloaded, image.AttachmentID)
		}
	}
	if len(unloaded) == 0 {
		return turns, nil
	}
	loaded, err := s.repo.GetAttachments(ctx, userID, unloaded)
	if err != nil {
		return nil, fmt.Errorf("loading attachments: %w", err)
	}
	return mapImages(turns, func(image domain.ImagePart) domain.ImagePart {
		if a, ok := loaded[image.AttachmentID]; ok && len(image.Data) == 0 {
			image.Data = a.Data
		}
		return image
	}), nil
}

// mapImages returns a copy of the turns with fn applied to each image they
// carry. The input turns are left unchanged.
func mapImages(turns []domain.Turn, fn func(domain.ImagePart) domain.ImagePart) []domain.Turn {
	mapped := make([]domain.Turn, len(turns))
	for i, turn := range turns {
		mapped[i] = domain.Turn{Role: turn.Role, Parts: make([]domain.Part, len(turn.Parts))}
		for j, part := range turn.Parts {
			switch {
			case part.Kind == domain.PartImage && part.Image != nil:
				image := fn(*part.Image)
				part.Image = &image
			case part.Kind == domain.PartToolResult && part.ToolResult != nil && len(part.ToolResult.Images) > 0:
				result := *part.ToolResult
				result.Images = make([]domain.ImagePart, len(part.ToolResult.Images))
				for k, image := range part.ToolResult.Images {
					result.Images[k] = fn(image)
				}
				part.ToolResult = &result
			}
			mapped[i].Parts[j] = part
		}
	}
	return mapped
}
