package question

import (
	"context"
	"errors"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

var (
	ErrQuestionNotFound  = errors.New("question not found")
	ErrQuestionForbidden = errors.New("question forbidden")
	ErrQuestionResolved  = errors.New("question already resolved")
)

// Broker manages pending user question requests independently of HTTP transport.
type Broker interface {
	Open(ctx context.Context, userID int64, request domain.UserQuestionRequest) (domain.UserQuestionRequest, error)
	Await(ctx context.Context, questionID string) ([]domain.UserQuestionAnswer, error)
	Resolve(ctx context.Context, userID int64, questionID string, answers []domain.UserQuestionAnswer) error
}
