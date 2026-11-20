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
	"github.com/devpad-org/devpad/internal/ai/question"
	"github.com/devpad-org/devpad/internal/auth"
)

const sseKeepAliveInterval = 15 * time.Second

// Handler holds HTTP handlers for AI endpoints.
type Handler struct {
	catalog     app.CatalogService
	agents      app.AgentService
	defaults    app.AgentDefaultsService
	budgets     app.BudgetDefaultsService
	summary     app.SummaryModelService
	usage       app.UsageService
	chat        app.ChatService
	runs        app.AgentRunService
	convService app.ConversationService
	attachments app.AttachmentService
	approvals   approval.Broker
	questions   question.Broker
}

// Services are the AI services a Handler serves.
type Services struct {
	Catalog       app.CatalogService
	Agents        app.AgentService
	AgentDefaults app.AgentDefaultsService
	Budgets       app.BudgetDefaultsService
	SummaryModel  app.SummaryModelService
	Usage         app.UsageService
	Chat          app.ChatService
	Runs          app.AgentRunService
	Conversations app.ConversationService
	Attachments   app.AttachmentService
	Approvals     approval.Broker
	Questions     question.Broker
}

// NewHandler creates a new AI handler.
func NewHandler(s Services) *Handler {
	return &Handler{
		catalog:     s.Catalog,
		agents:      s.Agents,
		defaults:    s.AgentDefaults,
		budgets:     s.Budgets,
		summary:     s.SummaryModel,
		usage:       s.Usage,
		chat:        s.Chat,
		runs:        s.Runs,
		convService: s.Conversations,
		attachments: s.Attachments,
		approvals:   s.Approvals,
		questions:   s.Questions,
	}
}

func isThinkingRequestError(err error) bool {
	return errors.Is(err, domain.ErrThinkingNotSupported) ||
		errors.Is(err, domain.ErrThinkingCannotBeDisabled) ||
		errors.Is(err, domain.ErrThinkingEffortNotSupported) ||
		errors.Is(err, domain.ErrThinkingEffortInvalid) ||
		errors.Is(err, domain.ErrThinkingEffortRequiresThinking)
}

// HandleListModels returns all available AI models with whether the user
// has a key for their provider, and the defaults the user set for built-in
// agents.
func (h *Handler) HandleListModels(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	models, err := h.catalog.ListModels(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list models")
		return
	}
	// Pickers fall back to devpad's own defaults, so models still list
	// without the user's.
	defaults, err := h.defaults.ListAgentDefaults(r.Context(), user.ID)
	if err != nil {
		log.Printf("listing agent defaults: %v", err)
		defaults = map[string]domain.AgentDefaults{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models, "agentDefaults": defaults})
}

// requireUser returns the signed-in user, or writes 401 and returns false.
func requireUser(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return nil, false
	}
	return user, true
}

// HandleListProviders returns every AI provider with whether the user has a
// key for it.
func (h *Handler) HandleListProviders(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	providers, err := h.catalog.ListProviders(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list providers")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

// HandleSetProviderKey saves the user's key for a provider.
func (h *Handler) HandleSetProviderKey(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		APIKey string `json:"apiKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err := h.catalog.SetProviderKey(r.Context(), user.ID, r.PathValue("id"), req.APIKey)
	switch {
	case errors.Is(err, domain.ErrProviderNotFound):
		writeError(w, http.StatusNotFound, "provider not found")
	case errors.Is(err, domain.ErrNoAPIKey):
		writeError(w, http.StatusBadRequest, "apiKey is required")
	case err != nil:
		log.Printf("failed to save provider key: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to save the provider key")
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// HandleRemoveProviderKey deletes the user's key for a provider.
func (h *Handler) HandleRemoveProviderKey(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	err := h.catalog.RemoveProviderKey(r.Context(), user.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, domain.ErrProviderNotFound):
		writeError(w, http.StatusNotFound, "provider not found")
	case err != nil:
		log.Printf("failed to remove provider key: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to remove the provider key")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleGetSummaryModel returns the model the user chose for
// summarize_file; "" means automatic.
func (h *Handler) HandleGetSummaryModel(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	model, err := h.summary.SummaryModel(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load the summary model")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"model": model})
}

// HandleSetSummaryModel sets the model summarize_file uses for the user;
// "" means automatic.
func (h *Handler) HandleSetSummaryModel(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	model, err := h.summary.SetSummaryModel(r.Context(), user.ID, req.Model)
	switch {
	case errors.Is(err, domain.ErrModelNotFound):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to save the summary model")
	default:
		writeJSON(w, http.StatusOK, map[string]string{"model": model})
	}
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

	stream, err := h.chat.StreamSimple(r.Context(), app.SimpleChatRequest{
		UserID:      user.ID,
		WorkspaceID: req.WorkspaceID,
		Model:       req.Model,
		Turns:       ToDomainTurns(req.Turns),
		Thinking:    ToDomainThinking(req.Thinking),
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrModelNotFound):
			writeError(w, http.StatusBadRequest, "model not found")
		case errors.Is(err, domain.ErrNoAPIKey), errors.Is(err, domain.ErrAPIKeyUnreadable):
			writeError(w, http.StatusBadRequest, err.Error())
		case isThinkingRequestError(err), isAttachmentError(err):
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

	model, thinking, err := h.withAgentDefaults(r.Context(), user.ID, req.AgentID, req.Model, ToDomainThinking(req.Thinking))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up the agent's default model")
		return
	}
	if model == "" {
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

	agentPrompt := ""
	if h.agents != nil {
		agent, err := h.agents.GetAgent(r.Context(), user.ID, req.WorkspaceID, req.AgentID)
		if err != nil {
			h.writeAgentError(w, err)
			return
		}
		agentPrompt = agent.Instructions
	}
	budget, err := h.assistantBudget(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up the assistant's budget")
		return
	}
	stream, err := h.chat.StreamAgent(r.Context(), app.AgentChatRequest{
		UserID:      user.ID,
		WorkspaceID: req.WorkspaceID,
		AgentPrompt: agentPrompt,
		Model:       model,
		Turns:       ToDomainTurns(req.Turns),
		Thinking:    thinking,
		Budget:      budget,
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrModelNotFound):
			writeError(w, http.StatusBadRequest, "model not found")
		case isThinkingRequestError(err), isAttachmentError(err):
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

// HandleAnswerQuestion resolves a pending AI question with the user's answers.
func (h *Handler) HandleAnswerQuestion(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if h.questions == nil {
		writeError(w, http.StatusServiceUnavailable, "question broker is not available")
		return
	}

	var req struct {
		ID      string              `json:"id"`
		Answers []QuestionAnswerDTO `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "question ID is required")
		return
	}

	if err := h.questions.Resolve(r.Context(), user.ID, req.ID, ToDomainQuestionAnswers(req.Answers)); err != nil {
		switch {
		case errors.Is(err, question.ErrQuestionNotFound):
			writeError(w, http.StatusNotFound, "no pending question with this ID")
		case errors.Is(err, question.ErrQuestionForbidden):
			writeError(w, http.StatusForbidden, "question does not belong to the authenticated user")
		case errors.Is(err, question.ErrQuestionResolved):
			writeError(w, http.StatusConflict, "question has already been resolved")
		default:
			writeError(w, http.StatusInternalServerError, "failed to resolve question")
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
