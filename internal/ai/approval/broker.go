package approval

import (
	"context"
	"errors"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

var (
	ErrApprovalNotFound  = errors.New("approval not found")
	ErrApprovalForbidden = errors.New("approval forbidden")
	ErrApprovalResolved  = errors.New("approval already resolved")
)

// Broker manages approval requests independently of HTTP transport.
type Broker interface {
	Open(ctx context.Context, userID int64, command string) (domain.ApprovalRequest, error)
	Await(ctx context.Context, approvalID string) (bool, error)
	Resolve(ctx context.Context, userID int64, approvalID string, approved bool) error
}
