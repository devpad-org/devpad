package app

import (
	"context"
	"errors"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

const validPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="

func TestConversationServiceSaveTurnsValidatesImages(t *testing.T) {
	repo := &fakeConversationRepository{
		conversation: &domain.Conversation{ID: 10, UserID: 1, WorkspaceID: 2, Model: "gpt-5.4"},
	}
	service := NewConversationService(repo, fakeModelLookup{model: domain.Model{ID: "gpt-5.4", Vision: true}})

	err := service.SaveTurns(context.Background(), 10, 1, []domain.Turn{{
		Role: domain.RoleUser,
		Parts: []domain.Part{{
			Kind: domain.PartImage,
			Image: &domain.ImagePart{
				MIMEType: "image/png",
				Data:     "",
			},
		}},
	}})

	if !errors.Is(err, domain.ErrInvalidImage) {
		t.Fatalf("expected invalid image error, got %v", err)
	}
	if repo.saved {
		t.Fatal("expected invalid image turns not to be persisted")
	}
}

func TestConversationServiceSaveTurnsRejectsImagesForNonVisionModel(t *testing.T) {
	repo := &fakeConversationRepository{
		conversation: &domain.Conversation{ID: 10, UserID: 1, WorkspaceID: 2, Model: "mistral-medium-3-5"},
	}
	service := NewConversationService(repo, fakeModelLookup{model: domain.Model{ID: "mistral-medium-3-5"}})

	err := service.SaveTurns(context.Background(), 10, 1, []domain.Turn{{
		Role: domain.RoleUser,
		Parts: []domain.Part{{
			Kind: domain.PartImage,
			Image: &domain.ImagePart{
				MIMEType: "image/png",
				Data:     validPNGBase64,
			},
		}},
	}})

	if !errors.Is(err, domain.ErrImagesNotSupported) {
		t.Fatalf("expected images not supported error, got %v", err)
	}
	if repo.saved {
		t.Fatal("expected non-vision image turns not to be persisted")
	}
}

func TestConversationServiceSaveTurnsRequiresModelLookupForImages(t *testing.T) {
	repo := &fakeConversationRepository{
		conversation: &domain.Conversation{ID: 10, UserID: 1, WorkspaceID: 2, Model: "gpt-5.4"},
	}
	service := NewConversationService(repo, nil)

	err := service.SaveTurns(context.Background(), 10, 1, []domain.Turn{{
		Role: domain.RoleUser,
		Parts: []domain.Part{{
			Kind: domain.PartImage,
			Image: &domain.ImagePart{
				MIMEType: "image/png",
				Data:     "aGk=",
			},
		}},
	}})

	if !errors.Is(err, ErrModelLookupNotConfigured) {
		t.Fatalf("expected model lookup configuration error, got %v", err)
	}
	if errors.Is(err, domain.ErrModelNotFound) {
		t.Fatalf("expected error not to masquerade as model not found, got %v", err)
	}
	if repo.saved {
		t.Fatal("expected image turns not to be persisted without model lookup")
	}
}

type fakeModelLookup struct {
	model domain.Model
	err   error
}

func (l fakeModelLookup) FindModel(string) (domain.Model, error) {
	if l.err != nil {
		return domain.Model{}, l.err
	}
	return l.model, nil
}

type fakeConversationRepository struct {
	conversation *domain.Conversation
	saved        bool
}

func (r *fakeConversationRepository) CreateConversation(_ context.Context, conv *domain.Conversation) error {
	conv.ID = 1
	return nil
}

func (r *fakeConversationRepository) GetConversation(_ context.Context, _, _ int64) (*domain.Conversation, error) {
	return r.conversation, nil
}

func (r *fakeConversationRepository) ListConversations(_ context.Context, _, _ int64) ([]domain.Conversation, error) {
	return nil, nil
}

func (r *fakeConversationRepository) UpdateConversation(_ context.Context, _, _ int64, _, _ string) error {
	return nil
}

func (r *fakeConversationRepository) DeleteConversation(_ context.Context, _, _ int64) error {
	return nil
}

func (r *fakeConversationRepository) SaveTurns(_ context.Context, _ int64, _ []domain.Turn) error {
	r.saved = true
	return nil
}

func (r *fakeConversationRepository) GetTurns(_ context.Context, _, _ int64) ([]domain.Turn, error) {
	return nil, nil
}
