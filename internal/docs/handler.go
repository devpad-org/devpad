package docs

import (
	"encoding/json"
	"net/http"

	"github.com/devpad-org/devpad/internal/auth"
)

// Handler serves the documentation to signed-in users. Admin pages are
// listed and served only to admins.
type Handler struct{}

// NewHandler creates the documentation handler.
func NewHandler() *Handler { return &Handler{} }

// HandleList lists the pages and their sections, for the docs sidebar.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": Pages(user.IsAdmin)})
}

// pageResponse is a page with its Markdown.
type pageResponse struct {
	Page
	Body string `json:"body"`
}

// HandleGet returns one page's Markdown.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	page, ok := Find(r.PathValue("slug"), user.IsAdmin)
	if !ok {
		writeError(w, http.StatusNotFound, "no such page")
		return
	}
	writeJSON(w, http.StatusOK, pageResponse{Page: page, Body: page.Body})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
