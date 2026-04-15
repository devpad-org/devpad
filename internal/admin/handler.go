package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/devpad-org/devpad/internal/auth"
)

// Handler holds HTTP handlers for admin endpoints.
type Handler struct {
	service Service
}

// NewHandler creates a new admin Handler.
func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

// HandleListUsers returns all users.
func (h *Handler) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.service.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	resp := make([]map[string]any, len(users))
	for i, u := range users {
		resp[i] = userResponse(u)
	}

	writeJSON(w, http.StatusOK, map[string]any{"users": resp})
}

// HandleCreateUser creates a new user.
func (h *Handler) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"isAdmin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Username == "" || req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username, email, and password are required")
		return
	}

	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	user, err := h.service.CreateUser(r.Context(), req.Username, req.Email, req.Password, req.IsAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"user": userResponse(user)})
}

// HandleUpdateUser updates an existing user.
func (h *Handler) HandleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		IsAdmin  bool   `json:"isAdmin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Username == "" || req.Email == "" {
		writeError(w, http.StatusBadRequest, "username and email are required")
		return
	}

	// Prevent admin from demoting themselves
	caller := auth.UserFromContext(r.Context())
	if caller != nil && caller.ID == id && caller.IsAdmin && !req.IsAdmin {
		writeError(w, http.StatusBadRequest, "cannot remove your own admin status")
		return
	}

	user, err := h.service.UpdateUser(r.Context(), id, req.Username, req.Email, req.IsAdmin)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"user": userResponse(user)})
}

// HandleResetPassword resets a user's password.
func (h *Handler) HandleResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	if err := h.service.ResetPassword(r.Context(), id, req.Password); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to reset password")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleDeleteUser deletes a user.
func (h *Handler) HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	caller := auth.UserFromContext(r.Context())
	if caller == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	if err := h.service.DeleteUser(r.Context(), caller.ID, id); err != nil {
		if errors.Is(err, ErrSelfDelete) {
			writeError(w, http.StatusBadRequest, "cannot delete your own account")
			return
		}
		if errors.Is(err, ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func parseIDParam(r *http.Request) (int64, error) {
	idStr := r.PathValue("id")
	return strconv.ParseInt(idStr, 10, 64)
}

func userResponse(u *auth.User) map[string]any {
	return map[string]any{
		"id":        u.ID,
		"username":  u.Username,
		"email":     u.Email,
		"isAdmin":   u.IsAdmin,
		"createdAt": u.CreatedAt,
		"updatedAt": u.UpdatedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
