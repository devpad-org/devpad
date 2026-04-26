package approval

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryBroker_OpenResolveAwait(t *testing.T) {
	broker := NewMemoryBroker()
	request, err := broker.Open(context.Background(), 7, "sudo apt update")
	if err != nil {
		t.Fatalf("open approval: %v", err)
	}
	if request.ID == "" {
		t.Fatal("expected generated approval ID")
	}
	if request.Command != "sudo apt update" {
		t.Fatalf("unexpected command: %q", request.Command)
	}

	if err := broker.Resolve(context.Background(), 7, request.ID, true); err != nil {
		t.Fatalf("resolve approval: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	approved, err := broker.Await(ctx, request.ID)
	if err != nil {
		t.Fatalf("await approval: %v", err)
	}
	if !approved {
		t.Fatal("expected approval decision to be true")
	}
}

func TestMemoryBroker_ResolveRejectsWrongUser(t *testing.T) {
	broker := NewMemoryBroker()
	request, err := broker.Open(context.Background(), 7, "sudo apt update")
	if err != nil {
		t.Fatalf("open approval: %v", err)
	}

	err = broker.Resolve(context.Background(), 8, request.ID, true)
	if !errors.Is(err, ErrApprovalForbidden) {
		t.Fatalf("expected ErrApprovalForbidden, got %v", err)
	}

	if err := broker.Resolve(context.Background(), 7, request.ID, false); err != nil {
		t.Fatalf("resolve approval after wrong user: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	approved, err := broker.Await(ctx, request.ID)
	if err != nil {
		t.Fatalf("await approval: %v", err)
	}
	if approved {
		t.Fatal("expected approval decision to be false")
	}
}

func TestMemoryBroker_ResolveRejectsDuplicateDecision(t *testing.T) {
	broker := NewMemoryBroker()
	request, err := broker.Open(context.Background(), 3, "sudo apt update")
	if err != nil {
		t.Fatalf("open approval: %v", err)
	}

	if err := broker.Resolve(context.Background(), 3, request.ID, true); err != nil {
		t.Fatalf("first resolve approval: %v", err)
	}
	err = broker.Resolve(context.Background(), 3, request.ID, false)
	if !errors.Is(err, ErrApprovalResolved) {
		t.Fatalf("expected ErrApprovalResolved, got %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	approved, err := broker.Await(ctx, request.ID)
	if err != nil {
		t.Fatalf("await approval: %v", err)
	}
	if !approved {
		t.Fatal("expected first decision to win")
	}
}
