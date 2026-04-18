package settings

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/devpad-org/devpad/internal/auth"
)

// Handler holds HTTP handlers for settings endpoints.
type Handler struct {
	service Service
}

// NewHandler creates a new settings Handler.
func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

// HandleChangePassword updates the authenticated user's password.
func (h *Handler) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "current password and new password are required")
		return
	}

	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}

	if err := h.service.ChangePassword(r.Context(), user.ID, req.CurrentPassword, req.NewPassword); err != nil {
		if errors.Is(err, ErrInvalidCurrentPassword) {
			writeError(w, http.StatusUnauthorized, "current password is incorrect")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to change password")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleGetMFAStatus returns whether TOTP is enabled for the user.
func (h *Handler) HandleGetMFAStatus(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	enabled, err := h.service.GetMFAStatus(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get MFA status")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"totpEnabled": enabled})
}

// HandleTOTPSetup generates a new TOTP secret and returns the provisioning URL.
func (h *Handler) HandleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	secret, url, err := h.service.GenerateTOTPSetup(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, ErrTOTPAlreadyEnabled) {
			writeError(w, http.StatusConflict, "TOTP is already enabled")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to generate TOTP setup")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"secret": secret,
		"url":    url,
	})
}

// HandleTOTPEnable verifies a TOTP code and enables MFA.
func (h *Handler) HandleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "TOTP code is required")
		return
	}

	if err := h.service.EnableTOTP(r.Context(), user.ID, req.Code); err != nil {
		if errors.Is(err, ErrInvalidTOTPCode) {
			writeError(w, http.StatusBadRequest, "invalid TOTP code")
			return
		}
		if errors.Is(err, ErrTOTPAlreadyEnabled) {
			writeError(w, http.StatusConflict, "TOTP is already enabled")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to enable TOTP")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleTOTPDisable disables TOTP MFA after password verification.
func (h *Handler) HandleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}

	if err := h.service.DisableTOTP(r.Context(), user.ID, req.Password); err != nil {
		if errors.Is(err, ErrInvalidCurrentPassword) {
			writeError(w, http.StatusUnauthorized, "incorrect password")
			return
		}
		if errors.Is(err, ErrTOTPNotEnabled) {
			writeError(w, http.StatusConflict, "TOTP is not enabled")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to disable TOTP")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleGetSSHKey returns the user's SSH public key.
func (h *Handler) HandleGetSSHKey(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	publicKey, err := h.service.GetSSHPublicKey(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get SSH key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"publicKey": publicKey})
}

// HandleGenerateSSHKey generates a new SSH key pair for the user.
func (h *Handler) HandleGenerateSSHKey(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	publicKey, err := h.service.GenerateSSHKey(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate SSH key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"publicKey": publicKey})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
