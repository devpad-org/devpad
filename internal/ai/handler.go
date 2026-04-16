package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/devpad-org/devpad/internal/auth"
)

// ToolExecutor executes AI agent tools against a workspace.
type ToolExecutor interface {
	ExecuteTool(ctx context.Context, userID, workspaceID int64, toolName string, args json.RawMessage) (string, error)
}

// Handler holds HTTP handlers for AI endpoints.
type Handler struct {
	service      Service
	toolExecutor ToolExecutor
}

// NewHandler creates a new AI handler.
func NewHandler(service Service, executor ToolExecutor) *Handler {
	return &Handler{service: service, toolExecutor: executor}
}

// HandleListModels returns all available AI models with their configuration status.
func (h *Handler) HandleListModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.service.ListModels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list models")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// HandleListProviders returns all registered AI providers with their config status (admin only).
func (h *Handler) HandleListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.service.ListProviders(r.Context())
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

	if err := h.service.UpdateProvider(r.Context(), providerID, req.APIKey, req.Enabled); err != nil {
		if errors.Is(err, ErrProviderNotFound) {
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

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages are required")
		return
	}

	stream, err := h.service.ChatStream(r.Context(), req)
	if err != nil {
		if errors.Is(err, ErrModelNotFound) {
			writeError(w, http.StatusBadRequest, "model not found")
			return
		}
		if errors.Is(err, ErrProviderNotEnabled) {
			writeError(w, http.StatusBadRequest, "AI provider is not enabled")
			return
		}
		if errors.Is(err, ErrNoAPIKey) {
			writeError(w, http.StatusBadRequest, "no API key configured for this provider")
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to start chat: %v", err))
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	for event := range stream {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
}

// maxToolIterations limits the number of tool call rounds to prevent infinite loops.
const maxToolIterations = 25

// HandleAgentChat handles a streaming chat with tool execution via SSE.
// This implements an agentic loop: the LLM can call tools, results are fed back,
// and the loop continues until the LLM produces a final response or hits the limit.
func (h *Handler) HandleAgentChat(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages are required")
		return
	}
	if req.WorkspaceID == 0 {
		writeError(w, http.StatusBadRequest, "workspaceId is required")
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	sendEvent := func(event StreamEvent) {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// Prepend system prompt and add tools
	messages := make([]Message, 0, len(req.Messages)+1)
	messages = append(messages, Message{Role: "system", Content: agentSystemPrompt})
	messages = append(messages, req.Messages...)
	tools := agentTools()

	for i := 0; i < maxToolIterations; i++ {
		chatReq := ChatRequest{
			Model:    req.Model,
			Messages: messages,
			Tools:    tools,
		}

		stream, err := h.service.ChatStream(r.Context(), chatReq)
		if err != nil {
			sendEvent(StreamEvent{Error: fmt.Sprintf("chat error: %v", err)})
			sendEvent(StreamEvent{Done: true})
			return
		}

		var toolCalls []ToolCall
		var contentAccum strings.Builder

		for event := range stream {
			if event.Error != "" {
				sendEvent(event)
				sendEvent(StreamEvent{Done: true})
				return
			}
			if event.Content != "" {
				sendEvent(StreamEvent{Content: event.Content})
				contentAccum.WriteString(event.Content)
			}
			if len(event.ToolCalls) > 0 {
				toolCalls = event.ToolCalls
			}
			// Don't forward Done yet — we may need to loop
		}

		// No tool calls — the LLM is done
		if len(toolCalls) == 0 {
			break
		}

		// Append assistant message with tool calls to conversation
		assistantMsg := Message{
			Role:      "assistant",
			ToolCalls: toolCalls,
		}
		if contentAccum.Len() > 0 {
			assistantMsg.Content = contentAccum.String()
		}
		messages = append(messages, assistantMsg)

		// Execute each tool and feed results back
		for _, tc := range toolCalls {
			// Notify frontend that a tool is being called
			sendEvent(StreamEvent{ToolCalls: []ToolCall{tc}})

			result, err := h.toolExecutor.ExecuteTool(
				r.Context(), user.ID, req.WorkspaceID,
				tc.Function.Name, json.RawMessage(tc.Function.Arguments),
			)
			if err != nil {
				result = fmt.Sprintf("Error executing tool: %v", err)
			}

			// Notify frontend of the result
			sendEvent(StreamEvent{ToolResult: &ToolResult{
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    result,
			}})

			// Append tool result message
			messages = append(messages, Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	sendEvent(StreamEvent{Done: true})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
