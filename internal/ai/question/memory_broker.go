package question

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

type pendingQuestion struct {
	userID  int64
	answers chan []domain.UserQuestionAnswer

	mu       sync.Mutex
	resolved bool
}

// MemoryBroker stores pending user questions in memory.
type MemoryBroker struct {
	pending sync.Map
}

// NewMemoryBroker creates a broker backed by in-memory state.
func NewMemoryBroker() *MemoryBroker {
	return &MemoryBroker{}
}

func (b *MemoryBroker) Open(_ context.Context, userID int64, request domain.UserQuestionRequest) (domain.UserQuestionRequest, error) {
	request.ID = generateQuestionID()
	b.pending.Store(request.ID, &pendingQuestion{
		userID:  userID,
		answers: make(chan []domain.UserQuestionAnswer, 1),
	})

	return request, nil
}

func (b *MemoryBroker) Await(ctx context.Context, questionID string) ([]domain.UserQuestionAnswer, error) {
	pending, ok := b.lookup(questionID)
	if !ok {
		return nil, ErrQuestionNotFound
	}
	defer b.pending.Delete(questionID)

	select {
	case answers := <-pending.answers:
		return cloneAnswers(answers), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *MemoryBroker) Resolve(_ context.Context, userID int64, questionID string, answers []domain.UserQuestionAnswer) error {
	pending, ok := b.lookup(questionID)
	if !ok {
		return ErrQuestionNotFound
	}
	if pending.userID != userID {
		return ErrQuestionForbidden
	}

	pending.mu.Lock()
	defer pending.mu.Unlock()
	if pending.resolved {
		return ErrQuestionResolved
	}
	pending.resolved = true
	pending.answers <- cloneAnswers(answers)

	return nil
}

func (b *MemoryBroker) lookup(questionID string) (*pendingQuestion, bool) {
	val, ok := b.pending.Load(questionID)
	if !ok {
		return nil, false
	}

	pending, ok := val.(*pendingQuestion)
	if !ok {
		return nil, false
	}

	return pending, true
}

func cloneAnswers(answers []domain.UserQuestionAnswer) []domain.UserQuestionAnswer {
	if answers == nil {
		return nil
	}
	cloned := make([]domain.UserQuestionAnswer, 0, len(answers))
	for _, answer := range answers {
		answer.Values = append([]string(nil), answer.Values...)
		cloned = append(cloned, answer)
	}
	return cloned
}

func generateQuestionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
