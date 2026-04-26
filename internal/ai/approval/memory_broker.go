package approval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

type pendingApproval struct {
	userID    int64
	decisions chan bool

	mu       sync.Mutex
	resolved bool
}

// MemoryBroker stores pending approvals in memory.
type MemoryBroker struct {
	pending sync.Map
}

// NewMemoryBroker creates a broker backed by in-memory state.
func NewMemoryBroker() *MemoryBroker {
	return &MemoryBroker{}
}

func (b *MemoryBroker) Open(_ context.Context, userID int64, command string) (domain.ApprovalRequest, error) {
	approvalID := generateApprovalID()
	b.pending.Store(approvalID, &pendingApproval{
		userID:    userID,
		decisions: make(chan bool, 1),
	})

	return domain.ApprovalRequest{ID: approvalID, Command: command}, nil
}

func (b *MemoryBroker) Await(ctx context.Context, approvalID string) (bool, error) {
	pending, ok := b.lookup(approvalID)
	if !ok {
		return false, ErrApprovalNotFound
	}
	defer b.pending.Delete(approvalID)

	select {
	case approved := <-pending.decisions:
		return approved, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (b *MemoryBroker) Resolve(_ context.Context, userID int64, approvalID string, approved bool) error {
	pending, ok := b.lookup(approvalID)
	if !ok {
		return ErrApprovalNotFound
	}
	if pending.userID != userID {
		return ErrApprovalForbidden
	}

	pending.mu.Lock()
	defer pending.mu.Unlock()
	if pending.resolved {
		return ErrApprovalResolved
	}
	pending.resolved = true
	pending.decisions <- approved

	return nil
}

func (b *MemoryBroker) lookup(approvalID string) (*pendingApproval, bool) {
	val, ok := b.pending.Load(approvalID)
	if !ok {
		return nil, false
	}

	pending, ok := val.(*pendingApproval)
	if !ok {
		return nil, false
	}

	return pending, true
}

func generateApprovalID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
