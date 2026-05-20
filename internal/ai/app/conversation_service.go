package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// ConversationRepository handles persistence of AI chat conversations and messages.
type ConversationRepository interface {
	CreateConversation(ctx context.Context, conv *domain.Conversation) error
	GetConversation(ctx context.Context, id, userID int64) (*domain.Conversation, error)
	ListConversations(ctx context.Context, userID, workspaceID int64) ([]domain.Conversation, error)
	UpdateConversation(ctx context.Context, id, userID int64, title, model string) error
	DeleteConversation(ctx context.Context, id, userID int64) error
	SaveTurns(ctx context.Context, conversationID int64, turns []domain.Turn) error
	GetTurns(ctx context.Context, conversationID, userID int64) ([]domain.Turn, error)
}

// ModelLookup resolves model capabilities needed when persisting rich chat turns.
type ModelLookup interface {
	FindModel(modelID string) (domain.Model, error)
}

// ConversationService defines business logic for managing chat conversations.
type ConversationService interface {
	CreateConversation(ctx context.Context, userID, workspaceID int64, model string) (*domain.Conversation, error)
	ListConversations(ctx context.Context, userID, workspaceID int64) ([]domain.Conversation, error)
	GetConversation(ctx context.Context, id, userID int64) (*domain.Conversation, error)
	DeleteConversation(ctx context.Context, id, userID int64) error
	SaveTurns(ctx context.Context, conversationID, userID int64, turns []domain.Turn) error
	GetTurns(ctx context.Context, conversationID, userID int64) ([]domain.Turn, error)
}

type conversationService struct {
	convRepo ConversationRepository
	models   ModelLookup
}

// NewConversationService creates a ConversationService backed by the given repository.
func NewConversationService(convRepo ConversationRepository) ConversationService {
	return &conversationService{convRepo: convRepo}
}

// NewConversationServiceWithModelLookup creates a ConversationService that can
// validate persisted chat history against model capabilities.
func NewConversationServiceWithModelLookup(convRepo ConversationRepository, models ModelLookup) ConversationService {
	return &conversationService{convRepo: convRepo, models: models}
}

func (s *conversationService) CreateConversation(ctx context.Context, userID, workspaceID int64, model string) (*domain.Conversation, error) {
	conv := &domain.Conversation{
		UserID:      userID,
		WorkspaceID: workspaceID,
		Title:       "",
		Model:       model,
	}
	if err := s.convRepo.CreateConversation(ctx, conv); err != nil {
		return nil, fmt.Errorf("creating conversation: %w", err)
	}

	return conv, nil
}

func (s *conversationService) ListConversations(ctx context.Context, userID, workspaceID int64) ([]domain.Conversation, error) {
	convs, err := s.convRepo.ListConversations(ctx, userID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing conversations: %w", err)
	}

	return convs, nil
}

func (s *conversationService) GetConversation(ctx context.Context, id, userID int64) (*domain.Conversation, error) {
	conv, err := s.convRepo.GetConversation(ctx, id, userID)
	if err != nil {
		return nil, fmt.Errorf("getting conversation: %w", err)
	}

	return conv, nil
}

func (s *conversationService) DeleteConversation(ctx context.Context, id, userID int64) error {
	if err := s.convRepo.DeleteConversation(ctx, id, userID); err != nil {
		return fmt.Errorf("deleting conversation: %w", err)
	}

	return nil
}

func (s *conversationService) SaveTurns(ctx context.Context, conversationID, userID int64, turns []domain.Turn) error {
	conv, err := s.convRepo.GetConversation(ctx, conversationID, userID)
	if err != nil {
		return fmt.Errorf("validating conversation ownership: %w", err)
	}
	if conv == nil {
		return domain.ErrConversationNotFound
	}
	if hasImageParts(turns) {
		if s.models == nil {
			return domain.ErrModelNotFound
		}
		model, err := s.models.FindModel(conv.Model)
		if err != nil {
			return err
		}
		if err := domain.ValidateImageRequest(model, domain.ChatRequest{Model: conv.Model, Turns: turns}); err != nil {
			return err
		}
	}

	newTitle := conv.Title
	if newTitle == "" {
		for _, turn := range turns {
			if turn.Role == domain.RoleUser && turn.Text() != "" {
				title := turn.Text()
				if len([]rune(title)) > 60 {
					title = string([]rune(title)[:60])
				}
				newTitle = strings.TrimSpace(title)
				break
			}
		}
	}

	if err := s.convRepo.SaveTurns(ctx, conversationID, turns); err != nil {
		return fmt.Errorf("saving turns: %w", err)
	}

	if err := s.convRepo.UpdateConversation(ctx, conversationID, userID, newTitle, conv.Model); err != nil {
		return fmt.Errorf("updating conversation metadata: %w", err)
	}

	return nil
}

func (s *conversationService) GetTurns(ctx context.Context, conversationID, userID int64) ([]domain.Turn, error) {
	turns, err := s.convRepo.GetTurns(ctx, conversationID, userID)
	if err != nil {
		return nil, fmt.Errorf("getting turns: %w", err)
	}

	return turns, nil
}

func hasImageParts(turns []domain.Turn) bool {
	for _, turn := range turns {
		for _, part := range turn.Parts {
			if part.Kind == domain.PartImage {
				return true
			}
		}
	}
	return false
}
