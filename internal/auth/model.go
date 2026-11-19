package auth

import "time"

// User represents a user account.
type User struct {
	ID            int64
	Username      string
	Email         string
	Password      string
	IsAdmin       bool
	TOTPSecret    string
	TOTPEnabled   bool
	SSHPublicKey  string
	SSHPrivateKey string
	// GitName and GitEmail are what the user chose to commit as; empty
	// means their Username or Email (see GitIdentity).
	GitName  string
	GitEmail string
	// GitIdentitySaved is set once the user has saved a git identity, even
	// an empty one. Until then devpad leaves the identity in repos it has
	// already seeded alone.
	GitIdentitySaved bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// GitIdentity returns the name and email the user's commits carry.
func (u *User) GitIdentity() (name, email string) {
	name, email = u.GitName, u.GitEmail
	if name == "" {
		name = u.Username
	}
	if email == "" {
		email = u.Email
	}
	return name, email
}

// Session represents an active user session.
type Session struct {
	ID        int64
	UserID    int64
	Token     string
	ExpiresAt time.Time
	CreatedAt time.Time
}
