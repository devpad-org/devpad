package question

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func TestMemoryBroker_OpenResolveAwait(t *testing.T) {
	broker := NewMemoryBroker()
	request, err := broker.Open(context.Background(), 7, domain.UserQuestionRequest{
		Title: "Choose stack",
		Questions: []domain.UserQuestion{{
			ID:     "stack",
			Prompt: "Which stack?",
			Type:   domain.UserQuestionSingleChoice,
		}},
	})
	if err != nil {
		t.Fatalf("open question: %v", err)
	}
	if request.ID == "" {
		t.Fatal("expected generated question ID")
	}

	answers := []domain.UserQuestionAnswer{{QuestionID: "stack", Values: []string{"go"}}}
	if err := broker.Resolve(context.Background(), 7, request.ID, answers); err != nil {
		t.Fatalf("resolve question: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := broker.Await(ctx, request.ID)
	if err != nil {
		t.Fatalf("await question: %v", err)
	}
	if len(got) != 1 || got[0].QuestionID != "stack" || len(got[0].Values) != 1 || got[0].Values[0] != "go" {
		t.Fatalf("unexpected answers: %+v", got)
	}
}

func TestMemoryBroker_ResolveRejectsWrongUser(t *testing.T) {
	broker := NewMemoryBroker()
	request, err := broker.Open(context.Background(), 7, domain.UserQuestionRequest{
		Questions: []domain.UserQuestion{{ID: "stack", Prompt: "Which stack?", Type: domain.UserQuestionText}},
	})
	if err != nil {
		t.Fatalf("open question: %v", err)
	}

	err = broker.Resolve(context.Background(), 8, request.ID, nil)
	if !errors.Is(err, ErrQuestionForbidden) {
		t.Fatalf("expected ErrQuestionForbidden, got %v", err)
	}

	if err := broker.Resolve(context.Background(), 7, request.ID, []domain.UserQuestionAnswer{{QuestionID: "stack", Custom: "Go"}}); err != nil {
		t.Fatalf("resolve question after wrong user: %v", err)
	}
}

func TestMemoryBroker_ResolveRejectsDuplicateDecision(t *testing.T) {
	broker := NewMemoryBroker()
	request, err := broker.Open(context.Background(), 3, domain.UserQuestionRequest{
		Questions: []domain.UserQuestion{{ID: "stack", Prompt: "Which stack?", Type: domain.UserQuestionText}},
	})
	if err != nil {
		t.Fatalf("open question: %v", err)
	}

	if err := broker.Resolve(context.Background(), 3, request.ID, []domain.UserQuestionAnswer{{QuestionID: "stack", Custom: "Go"}}); err != nil {
		t.Fatalf("first resolve question: %v", err)
	}
	err = broker.Resolve(context.Background(), 3, request.ID, []domain.UserQuestionAnswer{{QuestionID: "stack", Custom: "Node"}})
	if !errors.Is(err, ErrQuestionResolved) {
		t.Fatalf("expected ErrQuestionResolved, got %v", err)
	}
}
