package auth

import "time"

// User represents a user account.
type User struct {
	ID          int64
	Username    string
	Email       string
	Password    string
	IsAdmin     bool
	TOTPSecret  string
	TOTPEnabled bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Session represents an active user session.
type Session struct {
	ID        int64
	UserID    int64
	Token     string
	ExpiresAt time.Time
	CreatedAt time.Time
}
