package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const cookieName = "devpad_session"

// Handler holds HTTP handlers for authentication endpoints.
type Handler struct {
	service Service
}

// NewHandler creates a new auth Handler.
func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

// HandleSetupCheck returns whether initial setup is needed.
func (h *Handler) HandleSetupCheck(w http.ResponseWriter, r *http.Request) {
	needs, err := h.service.NeedsSetup(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check setup status")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needsSetup": needs})
}

// HandleSetup creates the initial admin user.
func (h *Handler) HandleSetup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
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

	user, err := h.service.Setup(r.Context(), req.Username, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrSetupCompleted) {
			writeError(w, http.StatusConflict, "setup already completed")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create admin user")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"user": userResponse(user),
	})
}

// HandleLogin authenticates a user and sets a session cookie.
func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTPCode string `json:"totpCode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	session, err := h.service.Login(r.Context(), req.Username, req.Password, req.TOTPCode)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		if errors.Is(err, ErrTOTPRequired) {
			writeJSON(w, http.StatusOK, map[string]any{"totpRequired": true})
			return
		}
		if errors.Is(err, ErrInvalidTOTP) {
			writeError(w, http.StatusUnauthorized, "invalid TOTP code")
			return
		}
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    session.Token,
		Path:     "/",
		Expires:  session.ExpiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	user, _ := h.service.ValidateSession(r.Context(), session.Token)
	writeJSON(w, http.StatusOK, map[string]any{
		"user": userResponse(user),
	})
}

// HandleLogout destroys the current session.
func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	_ = h.service.Logout(r.Context(), cookie.Value)

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleMe returns the currently authenticated user.
func (h *Handler) HandleMe(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": userResponse(user),
	})
}

func userResponse(u *User) map[string]any {
	if u == nil {
		return nil
	}
	return map[string]any{
		"id":          u.ID,
		"username":    u.Username,
		"email":       u.Email,
		"isAdmin":     u.IsAdmin,
		"totpEnabled": u.TOTPEnabled,
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
