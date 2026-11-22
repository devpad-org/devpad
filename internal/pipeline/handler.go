package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/project"
)

// Settings keeps a project's pipeline setting, for the user whose project
// it is.
type Settings interface {
	SetPipelineConfig(ctx context.Context, userID, projectID int64, config string) (*project.Project, error)
}

// Handler holds the HTTP handlers for a project's pipeline.
type Handler struct {
	settings Settings
}

// NewHandler creates a Handler.
func NewHandler(settings Settings) *Handler {
	return &Handler{settings: settings}
}

// HandleSetConfig sets the pipeline a project's commits without a pipeline
// file run, refusing YAML that does not parse. Blank clears it. It answers
// with the project, as the project's other settings do.
func (h *Handler) HandleSetConfig(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	var req struct {
		Config string `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := ValidateSetting(req.Config); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config := req.Config
	if strings.TrimSpace(config) == "" {
		config = ""
	}
	p, err := h.settings.SetPipelineConfig(r.Context(), user.ID, projectID, config)
	if err != nil {
		switch {
		case errors.Is(err, project.ErrNotFound):
			writeError(w, http.StatusNotFound, "not found")
		case errors.Is(err, project.ErrForbidden):
			writeError(w, http.StatusForbidden, "access denied")
		default:
			log.Printf("project %d: setting the pipeline: %v", projectID, err)
			writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project.Response(p)})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
