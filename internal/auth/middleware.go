package auth

import (
	"context"
	"net/http"
)

type contextKey string

// UserContextKey is exported for use in tests that need to inject a user into context.
const UserContextKey contextKey = "auth_user"

// Middleware provides HTTP middleware for authentication.
type Middleware struct {
	service Service
}

// NewMiddleware creates a new auth Middleware.
func NewMiddleware(service Service) *Middleware {
	return &Middleware{service: service}
}

// RequireAuth rejects requests without a valid session.
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		user, err := m.service.ValidateSession(r.Context(), cookie.Value)
		if err != nil || user == nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin rejects requests from non-admin users. Must be used after RequireAuth.
func (m *Middleware) RequireAdmin(next http.Handler) http.Handler {
	return m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil || !user.IsAdmin {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// UserFromContext extracts the authenticated user from the request context.
func UserFromContext(ctx context.Context) *User {
	user, _ := ctx.Value(UserContextKey).(*User)
	return user
}
