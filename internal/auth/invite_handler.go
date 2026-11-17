package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// InviteHandler serves invites: admins create, list and revoke them, and
// anyone holding a token can check it and accept it.
type InviteHandler struct {
	invites InviteService
	auth    *Handler
}

// NewInviteHandler creates an InviteHandler. auth supplies the session
// cookie, so it must be the server's own auth Handler.
func NewInviteHandler(invites InviteService, auth *Handler) *InviteHandler {
	return &InviteHandler{invites: invites, auth: auth}
}

func inviteResponse(inv *Invite, now time.Time) map[string]any {
	return map[string]any{
		"id":        inv.ID,
		"email":     inv.Email,
		"isAdmin":   inv.IsAdmin,
		"createdBy": inv.CreatedByName,
		"expiresAt": inv.ExpiresAt,
		"createdAt": inv.CreatedAt,
		"expired":   inv.Expired(now),
	}
}

// HandleCreate makes an invite. Its token is in this response only.
func (h *InviteHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	caller := UserFromContext(r.Context())
	if caller == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		Email     string `json:"email"`
		IsAdmin   bool   `json:"isAdmin"`
		ValidDays int    `json:"validDays"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	maxDays := int(MaxInviteTTL / (24 * time.Hour))
	if req.ValidDays < 0 || req.ValidDays > maxDays {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("an invite can last at most %d days", maxDays))
		return
	}

	inv, token, err := h.invites.Create(r.Context(), caller.ID, req.Email, req.IsAdmin, time.Duration(req.ValidDays)*24*time.Hour)
	if err != nil {
		switch {
		case errors.Is(err, ErrEmailTaken):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, ErrInviteTTL):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to create invite")
		}
		return
	}
	inv.CreatedByName = caller.Username

	resp := inviteResponse(inv, time.Now())
	resp["token"] = token
	writeJSON(w, http.StatusCreated, map[string]any{"invite": resp})
}

// HandleList returns the invites nobody has used yet, expired ones included.
func (h *InviteHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	invites, err := h.invites.ListPending(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list invites")
		return
	}
	now := time.Now()
	resp := make([]map[string]any, len(invites))
	for i, inv := range invites {
		resp[i] = inviteResponse(inv, now)
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": resp})
}

// HandleRevoke deletes an unused invite, so its link stops working.
func (h *InviteHandler) HandleRevoke(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid invite id")
		return
	}
	if err := h.invites.Revoke(r.Context(), id); err != nil {
		if errors.Is(err, ErrInviteNotFound) {
			writeError(w, http.StatusNotFound, "invite not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to revoke invite")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleCheck tells the invite page whether a token can still be used, and
// what it will make. The token comes in the body, never the URL, so it stays
// out of logs.
func (h *InviteHandler) HandleCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	inv, err := h.invites.Check(r.Context(), req.Token)
	if err != nil {
		if errors.Is(err, ErrInviteInvalid) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to check invite")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"invite": map[string]any{
			"email":     inv.Email,
			"isAdmin":   inv.IsAdmin,
			"invitedBy": inv.CreatedByName,
			"expiresAt": inv.ExpiresAt,
		},
	})
}

// HandleAccept creates the invitee's account and signs them in. Someone
// already signed in must sign out first, so an invite is never accepted by
// accident in a browser that belongs to another account.
func (h *InviteHandler) HandleAccept(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(h.auth.cookieName); err == nil {
		if user, err := h.auth.service.ValidateSession(r.Context(), cookie.Value); err == nil && user != nil {
			writeError(w, http.StatusConflict, "you are signed in as "+user.Username+"; sign out to accept this invite")
			return
		}
	}

	var req struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Username) == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}
	if len(req.Password) < MinPasswordLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("password must be at least %d characters", MinPasswordLength))
		return
	}
	if len(req.Password) > MaxPasswordLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("password must be at most %d bytes", MaxPasswordLength))
		return
	}

	user, session, err := h.invites.Accept(r.Context(), req.Token, req.Username, req.Email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrInviteInvalid):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrUserExists):
			writeError(w, http.StatusConflict, "that username or email is already taken")
		case errors.Is(err, ErrEmailRequired):
			writeError(w, http.StatusBadRequest, err.Error())
		case user != nil:
			writeError(w, http.StatusInternalServerError, "account created but failed to sign in; sign in with your new password")
		default:
			writeError(w, http.StatusInternalServerError, "failed to accept invite")
		}
		return
	}
	h.auth.setSessionCookie(w, session)
	writeJSON(w, http.StatusCreated, map[string]any{"user": userResponse(user)})
}
