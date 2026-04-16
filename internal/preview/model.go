package preview

import "time"

// Token represents a short-lived, single-use token for authenticating preview access.
type Token struct {
	ID          int64
	Token       string
	UserID      int64
	WorkspaceID int64
	Port        int
	Used        bool
	ExpiresAt   time.Time
	CreatedAt   time.Time
}
