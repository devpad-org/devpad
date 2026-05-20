package httptransport

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/devpad-org/devpad/internal/ai/app"
	"github.com/devpad-org/devpad/internal/ai/approval"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/auth"
)

const sseKeepAliveInterval = 15 * time.Second

// Handler holds HTTP handlers for AI endpoints.
type Handler struct {
	catalog     app.CatalogService
	agents      app.AgentService
	chat        app.ChatService
	runs        app.AgentRunService
	convService app.ConversationService
	approvals   approval.Broker
}

// NewHandler creates a new AI handler.
func NewHandler(catalog app.CatalogService, agents app.AgentService, chat app.ChatService, runs app.AgentRunService, convService app.ConversationService, approvals approval.Broker) *Handler {
	return &Handler{
		catalog:     catalog,
		agents:      agents,
		chat:        chat,
		runs:        runs,
		convService: convService,
		approvals:   approvals,
	}
}

func isThinkingRequestError(err error) bool {
	return errors.Is(err, domain.ErrThinkingNotSupported) ||
		errors.Is(err, domain.ErrThinkingCannotBeDisabled) ||
		errors.Is(err, domain.ErrThinkingEffortNotSupported) ||
		errors.Is(err, domain.ErrThinkingEffortInvalid) ||
		errors.Is(err, domain.ErrThinkingEffortRequiresThinking)
}

func isImageRequestError(err error) bool {
	return errors.Is(err, domain.ErrImagesNotSupported) ||
		errors.Is(err, domain.ErrImageTooLarge) ||
		errors.Is(err, domain.ErrInvalidImage)
}

// HandleListModels returns all available AI models with their configuration status.
func (h *Handler) HandleListModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.catalog.ListModels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list models")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// HandleListProviders returns all registered AI providers with their config status (admin only).
func (h *Handler) HandleListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.catalog.ListProviders(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list providers")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

// HandleUpdateProvider updates an AI provider's configuration (admin only).
func (h *Handler) HandleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	providerID := r.PathValue("id")
	if providerID == "" {
		writeError(w, http.StatusBadRequest, "provider ID is required")
		return
	}

	var req struct {
		APIKey  string `json:"apiKey"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.catalog.UpdateProvider(r.Context(), providerID, req.APIKey, req.Enabled); err != nil {
		if errors.Is(err, domain.ErrProviderNotFound) {
			writeError(w, http.StatusNotFound, "provider not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update provider")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleChat handles a streaming chat completion request via SSE.
func (h *Handler) HandleChat(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req ChatRequestDTO
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
	turns := ToDomainTurns(req.Turns)
	if !h.validateImageRequest(w, r, req.Model, turns) {
		return
	}

	stream, err := h.chat.StreamSimple(r.Context(), app.SimpleChatRequest{
		UserID:   user.ID,
		Model:    req.Model,
		Turns:    turns,
		Thinking: ToDomainThinking(req.Thinking),
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrModelNotFound):
			writeError(w, http.StatusBadRequest, "model not found")
		case errors.Is(err, domain.ErrProviderNotEnabled):
			writeError(w, http.StatusBadRequest, "AI provider is not enabled")
		case errors.Is(err, domain.ErrNoAPIKey):
			writeError(w, http.StatusBadRequest, "no API key configured for this provider")
		case isThinkingRequestError(err):
			writeError(w, http.StatusBadRequest, err.Error())
		case isImageRequestError(err):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			log.Printf("failed to start chat: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to start chat")
		}
		return
	}

	setSSEHeaders(w)
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	for event := range stream {
		if err := writeSSEEvent(w, flusher, FromClientEvent(event)); err != nil {
			log.Printf("SSE write error: %v", err)
			return
		}
	}
}

// HandleAgentChat handles a streaming chat with tool execution via SSE.
func (h *Handler) HandleAgentChat(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req ChatRequestDTO
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
	if req.WorkspaceID == 0 {
		writeError(w, http.StatusBadRequest, "workspaceId is required")
		return
	}
	turns := ToDomainTurns(req.Turns)
	if !h.validateImageRequest(w, r, req.Model, turns) {
		return
	}

	agentPrompt := ""
	if h.agents != nil {
		agent, err := h.agents.GetAgent(r.Context(), user.ID, req.WorkspaceID, req.AgentID)
		if err != nil {
			h.writeAgentError(w, err)
			return
		}
		agentPrompt = agent.Instructions
	}
	stream, err := h.chat.StreamAgent(r.Context(), app.AgentChatRequest{
		UserID:      user.ID,
		WorkspaceID: req.WorkspaceID,
		AgentPrompt: agentPrompt,
		Model:       req.Model,
		Turns:       turns,
		Thinking:    ToDomainThinking(req.Thinking),
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrModelNotFound):
			writeError(w, http.StatusBadRequest, "model not found")
		case isThinkingRequestError(err):
			writeError(w, http.StatusBadRequest, err.Error())
		case isImageRequestError(err):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to start agent chat")
		}
		return
	}

	setSSEHeaders(w)
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	keepAliveTicker := time.NewTicker(sseKeepAliveInterval)
	defer keepAliveTicker.Stop()

	for {
		select {
		case event, ok := <-stream:
			if !ok {
				return
			}
			if err := writeSSEEvent(w, flusher, FromClientEvent(event)); err != nil {
				log.Printf("SSE write error: %v", err)
				return
			}
			keepAliveTicker.Reset(sseKeepAliveInterval)
		case <-keepAliveTicker.C:
			if err := writeSSEComment(w, flusher, "keepalive"); err != nil {
				log.Printf("SSE keepalive write error: %v", err)
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Handler) validateImageRequest(w http.ResponseWriter, r *http.Request, modelID string, turns []domain.Turn) bool {
	if !hasImageParts(turns) {
		return true
	}
	if h.catalog == nil {
		writeError(w, http.StatusInternalServerError, "failed to validate model")
		return false
	}
	model, err := h.catalog.FindModel(modelID)
	if err != nil {
		if errors.Is(err, domain.ErrModelNotFound) {
			writeError(w, http.StatusBadRequest, "model not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to validate model")
		}
		return false
	}
	if err := domain.ValidateImageRequest(model, domain.ChatRequest{Model: modelID, Turns: turns}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func hasImageParts(turns []domain.Turn) bool {
	for _, turn := range turns {
		for _, part := range turn.Parts {
			if part.Kind == domain.PartImage {
				return true
			}
		}
	}
	return false
}

// HandleApproveCommand handles user approval or denial of a sudo command.
func (h *Handler) HandleApproveCommand(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		ID       string `json:"id"`
		Approved bool   `json:"approved"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "approval ID is required")
		return
	}

	if err := h.approvals.Resolve(r.Context(), user.ID, req.ID, req.Approved); err != nil {
		switch {
		case errors.Is(err, approval.ErrApprovalNotFound):
			writeError(w, http.StatusNotFound, "no pending approval with this ID")
		case errors.Is(err, approval.ErrApprovalForbidden):
			writeError(w, http.StatusForbidden, "approval does not belong to the authenticated user")
		case errors.Is(err, approval.ErrApprovalResolved):
			writeError(w, http.StatusConflict, "approval has already been resolved")
		default:
			writeError(w, http.StatusInternalServerError, "failed to resolve approval")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleListConversations returns all conversations for the authenticated user in a workspace.
func (h *Handler) HandleListConversations(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	workspaceIDStr := r.URL.Query().Get("workspaceId")
	if workspaceIDStr == "" {
		writeError(w, http.StatusBadRequest, "workspaceId is required")
		return
	}
	var workspaceID int64
	if _, err := fmt.Sscanf(workspaceIDStr, "%d", &workspaceID); err != nil || workspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspaceId must be a positive integer")
		return
	}

	conversations, err := h.convService.ListConversations(r.Context(), user.ID, workspaceID)
	if err != nil {
		log.Printf("failed to list conversations: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list conversations")
		return
	}

	if conversations == nil {
		conversations = []domain.Conversation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": conversations})
}

// HandleCreateConversation creates a new conversation for the authenticated user.
func (h *Handler) HandleCreateConversation(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		WorkspaceID int64  `json:"workspaceId"`
		Model       string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.WorkspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspaceId is required")
		return
	}

	conversation, err := h.convService.CreateConversation(r.Context(), user.ID, req.WorkspaceID, req.Model)
	if err != nil {
		log.Printf("failed to create conversation: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create conversation")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"conversation": conversation})
}

// HandleDeleteConversation deletes a conversation owned by the authenticated user.
func (h *Handler) HandleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var conversationID int64
	if _, err := fmt.Sscanf(r.PathValue("id"), "%d", &conversationID); err != nil || conversationID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid conversation ID")
		return
	}

	if err := h.convService.DeleteConversation(r.Context(), conversationID, user.ID); err != nil {
		log.Printf("failed to delete conversation %d: %v", conversationID, err)
		writeError(w, http.StatusInternalServerError, "failed to delete conversation")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleGetMessages returns all messages for a conversation owned by the authenticated user.
func (h *Handler) HandleGetMessages(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var conversationID int64
	if _, err := fmt.Sscanf(r.PathValue("id"), "%d", &conversationID); err != nil || conversationID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid conversation ID")
		return
	}

	turns, err := h.convService.GetTurns(r.Context(), conversationID, user.ID)
	if err != nil {
		log.Printf("failed to get messages for conversation %d: %v", conversationID, err)
		writeError(w, http.StatusInternalServerError, "failed to get messages")
		return
	}

	turnDTOs := FromDomainTurns(turns)
	if turnDTOs == nil {
		turnDTOs = []TurnDTO{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"turns": turnDTOs})
}

// HandleSaveMessages replaces all messages for a conversation.
func (h *Handler) HandleSaveMessages(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var conversationID int64
	if _, err := fmt.Sscanf(r.PathValue("id"), "%d", &conversationID); err != nil || conversationID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid conversation ID")
		return
	}

	var req struct {
		Turns []TurnDTO `json:"turns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.convService.SaveTurns(r.Context(), conversationID, user.ID, ToDomainTurns(req.Turns)); err != nil {
		if errors.Is(err, domain.ErrConversationNotFound) {
			writeError(w, http.StatusNotFound, "conversation not found")
			return
		}
		if errors.Is(err, domain.ErrModelNotFound) {
			writeError(w, http.StatusBadRequest, "model not found")
			return
		}
		if isImageRequestError(err) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("failed to save messages for conversation %d: %v", conversationID, err)
		writeError(w, http.StatusInternalServerError, "failed to save messages")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
