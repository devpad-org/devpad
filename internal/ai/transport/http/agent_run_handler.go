package httptransport

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/devpad-org/devpad/internal/ai/app"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/auth"
)

// HandleCreateAgentRun starts a browser-independent agent run.
func (h *Handler) HandleCreateAgentRun(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if h.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "agent runner is not available")
		return
	}

	var req CreateAgentRunRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(req.Turns) == 0 {
		writeError(w, http.StatusBadRequest, "turns are required")
		return
	}
	if req.WorkspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspaceId is required")
		return
	}

	run, err := h.runs.StartRun(r.Context(), app.StartAgentRunRequest{
		UserID:         user.ID,
		WorkspaceID:    req.WorkspaceID,
		ConversationID: req.ConversationID,
		ParentRunID:    req.ParentRunID,
		AgentID:        req.AgentID,
		Model:          req.Model,
		Turns:          ToDomainTurns(req.Turns),
		Thinking:       ToDomainThinking(req.Thinking),
	})
	if err != nil {
		h.writeAgentRunError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"run": FromAgentRun(run)})
}

// HandleListAgentRuns returns background agent runs for a workspace.
func (h *Handler) HandleListAgentRuns(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if h.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "agent runner is not available")
		return
	}

	workspaceID, ok := parsePositiveQueryInt(w, r, "workspaceId", "workspaceId is required")
	if !ok {
		return
	}

	runs, err := h.runs.ListRuns(r.Context(), user.ID, workspaceID)
	if err != nil {
		h.writeAgentRunError(w, err)
		return
	}
	runDTOs := make([]AgentRunDTO, 0, len(runs))
	for _, run := range runs {
		run := run
		runDTOs = append(runDTOs, FromAgentRunSummary(&run))
	}

	writeJSON(w, http.StatusOK, map[string]any{"runs": runDTOs})
}

// HandleGetAgentRun returns metadata for a background agent run owned by the user.
func (h *Handler) HandleGetAgentRun(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if h.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "agent runner is not available")
		return
	}

	runID, ok := parsePositivePathID(w, r, "id", "invalid agent run ID")
	if !ok {
		return
	}

	run, err := h.runs.GetRun(r.Context(), user.ID, runID)
	if err != nil {
		h.writeAgentRunError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"run": FromAgentRun(run)})
}

// HandleAgentRunEvents streams persisted and live events for a background run.
func (h *Handler) HandleAgentRunEvents(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if h.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "agent runner is not available")
		return
	}

	runID, ok := parsePositivePathID(w, r, "id", "invalid agent run ID")
	if !ok {
		return
	}
	afterSequence, ok := parseNonNegativeQueryInt(w, r, "after", "after must be a non-negative integer")
	if !ok {
		return
	}

	stream, err := h.runs.SubscribeEvents(r.Context(), user.ID, runID, afterSequence)
	if err != nil {
		h.writeAgentRunError(w, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	setSSEHeaders(w)
	w.WriteHeader(http.StatusOK)

	keepAliveTicker := time.NewTicker(sseKeepAliveInterval)
	defer keepAliveTicker.Stop()

	for {
		select {
		case event, ok := <-stream:
			if !ok {
				return
			}
			if err := writeSSEEvent(w, flusher, FromAgentRunEvent(event)); err != nil {
				log.Printf("agent run SSE write error: %v", err)
				return
			}
			keepAliveTicker.Reset(sseKeepAliveInterval)
			if event.Event.Done {
				return
			}
		case <-keepAliveTicker.C:
			if err := writeSSEComment(w, flusher, "keepalive"); err != nil {
				log.Printf("agent run SSE keepalive write error: %v", err)
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// HandleCancelAgentRun cancels an active background agent run.
func (h *Handler) HandleCancelAgentRun(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if h.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "agent runner is not available")
		return
	}

	runID, ok := parsePositivePathID(w, r, "id", "invalid agent run ID")
	if !ok {
		return
	}
	if err := h.runs.CancelRun(r.Context(), user.ID, runID); err != nil {
		h.writeAgentRunError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) writeAgentRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrAgentRunNotFound):
		writeError(w, http.StatusNotFound, "agent run not found")
	case errors.Is(err, domain.ErrAgentRunNotActive):
		writeError(w, http.StatusConflict, "agent run is not active")
	case errors.Is(err, domain.ErrAgentNotFound):
		writeError(w, http.StatusNotFound, "agent not found")
	case errors.Is(err, domain.ErrModelNotFound):
		writeError(w, http.StatusBadRequest, "model not found")
	case errors.Is(err, domain.ErrProviderNotEnabled):
		writeError(w, http.StatusBadRequest, "AI provider is not enabled")
	case errors.Is(err, domain.ErrNoAPIKey):
		writeError(w, http.StatusBadRequest, "no API key configured for this provider")
	case errors.Is(err, domain.ErrThinkingNotSupported), errors.Is(err, domain.ErrThinkingCannotBeDisabled):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		log.Printf("agent run error: %v", err)
		writeError(w, http.StatusInternalServerError, "agent run failed")
	}
}

func parsePositivePathID(w http.ResponseWriter, r *http.Request, name, message string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, message)
		return 0, false
	}
	return id, true
}

func parseNonNegativeQueryInt(w http.ResponseWriter, r *http.Request, name, message string) (int64, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		writeError(w, http.StatusBadRequest, message)
		return 0, false
	}
	return value, true
}

func parsePositiveQueryInt(w http.ResponseWriter, r *http.Request, name, message string) (int64, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		writeError(w, http.StatusBadRequest, message)
		return 0, false
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		writeError(w, http.StatusBadRequest, message)
		return 0, false
	}
	return value, true
}
