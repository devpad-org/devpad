package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/devpad-org/devpad/internal/auth"
)

// ToolExecutor executes AI agent tools against a workspace.
type ToolExecutor interface {
	ExecuteTool(ctx context.Context, userID, workspaceID int64, toolName string, args json.RawMessage) (string, error)
}

// Handler holds HTTP handlers for AI endpoints.
type Handler struct {
	service          Service
	toolExecutor     ToolExecutor
	pendingApprovals sync.Map // map[string]chan bool
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
		log.Printf("failed to start chat: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to start chat")
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
		data, err := json.Marshal(event)
		if err != nil {
			log.Printf("failed to marshal SSE event: %v", err)
			continue
		}
		if _, writeErr := fmt.Fprintf(w, "data: %s\n\n", data); writeErr != nil {
			log.Printf("SSE write error: %v", writeErr)
			return
		}
		flusher.Flush()
	}
}

// maxToolIterations limits the number of tool call rounds to prevent infinite loops.
const maxToolIterations = 25

// sseKeepAliveInterval is how often a keepalive comment is sent during silent periods.
const sseKeepAliveInterval = 15 * time.Second

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

	// writeFailed tracks whether writing to the client has failed.
	writeFailed := false

	sendEvent := func(event StreamEvent) {
		if writeFailed {
			return
		}
		data, err := json.Marshal(event)
		if err != nil {
			log.Printf("failed to marshal SSE event: %v", err)
			return
		}
		if _, writeErr := fmt.Fprintf(w, "data: %s\n\n", data); writeErr != nil {
			log.Printf("SSE write error: %v", writeErr)
			writeFailed = true
			return
		}
		flusher.Flush()
	}

	// sendKeepAlive sends an SSE comment to keep the connection alive.
	sendKeepAlive := func() {
		if writeFailed {
			return
		}
		if _, writeErr := fmt.Fprintf(w, ": keepalive\n\n"); writeErr != nil {
			log.Printf("SSE keepalive write error: %v", writeErr)
			writeFailed = true
			return
		}
		flusher.Flush()
	}

	// Start keepalive ticker — sends heartbeat comments during silent periods.
	keepAliveTicker := time.NewTicker(sseKeepAliveInterval)
	defer keepAliveTicker.Stop()

	// drainKeepAlive resets the ticker. Call after sending a real event.
	drainKeepAlive := func() {
		keepAliveTicker.Reset(sseKeepAliveInterval)
	}

	// Prepend system prompt and add tools
	messages := make([]Message, 0, len(req.Messages)+1)
	messages = append(messages, Message{Role: "system", Content: agentSystemPrompt})
	messages = append(messages, req.Messages...)
	tools := agentTools()

	for i := 0; i < maxToolIterations; i++ {
		if writeFailed {
			return
		}

		chatReq := ChatRequest{
			Model:    req.Model,
			Messages: messages,
			Tools:    tools,
		}

		stream, err := h.service.ChatStream(r.Context(), chatReq)
		if err != nil {
			log.Printf("agent chat stream error: %v", err)
			sendEvent(StreamEvent{Error: "chat error"})
			sendEvent(StreamEvent{Done: true})
			return
		}

		var toolCalls []ToolCall
		var contentAccum strings.Builder

		// Read from the stream with keepalive during pauses.
		streamDone := false
		for !streamDone {
			select {
			case event, ok := <-stream:
				if !ok {
					streamDone = true
					break
				}
				drainKeepAlive()
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
			case <-keepAliveTicker.C:
				sendKeepAlive()
			case <-r.Context().Done():
				return
			}
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
			if writeFailed {
				return
			}

			// Notify frontend that a tool is being called
			sendEvent(StreamEvent{ToolCalls: []ToolCall{tc}})
			drainKeepAlive()

			// Check if this is a sudo command that needs approval
			if tc.Function.Name == "run_command" && commandNeedsSudoApproval(tc.Function.Arguments) {
				result, approved := h.requestApproval(r.Context(), sendEvent, tc.Function.Arguments)
				if !approved {
					sendEvent(StreamEvent{ToolResult: &ToolResult{
						ToolCallID: tc.ID,
						Name:       tc.Function.Name,
						Content:    result,
					}})
					messages = append(messages, Message{
						Role:       "tool",
						Content:    result,
						ToolCallID: tc.ID,
					})
					continue
				}
			}

			// Send keepalives while the tool executes.
			toolDone := make(chan struct{})
			var toolResult string
			var toolErr error
			go func() {
				defer close(toolDone)
				toolResult, toolErr = h.toolExecutor.ExecuteTool(
					r.Context(), user.ID, req.WorkspaceID,
					tc.Function.Name, json.RawMessage(tc.Function.Arguments),
				)
			}()

		toolWait:
			for {
				select {
				case <-toolDone:
					break toolWait
				case <-keepAliveTicker.C:
					sendKeepAlive()
				case <-r.Context().Done():
					return
				}
			}
			drainKeepAlive()

			result := toolResult
			if toolErr != nil {
				log.Printf("error executing tool %s: %v", tc.Function.Name, toolErr)
				result = "Error executing tool"
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

		// Warn if we're about to hit the iteration limit
		if i == maxToolIterations-1 {
			sendEvent(StreamEvent{Content: "\n\n⚠️ Reached the maximum number of tool call iterations (" + fmt.Sprintf("%d", maxToolIterations) + "). Stopping here."})
		}
	}

	sendEvent(StreamEvent{Done: true})
}

const approvalTimeout = 60 * time.Second

// commandNeedsSudoApproval checks if a run_command argument contains sudo.
func commandNeedsSudoApproval(args string) bool {
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return false
	}
	// Check for sudo as a standalone command/word
	fields := strings.Fields(params.Command)
	for _, f := range fields {
		if f == "sudo" {
			return true
		}
	}
	return false
}

// generateApprovalID creates a random approval ID.
func generateApprovalID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// requestApproval sends an approval request to the frontend and blocks until
// the user responds or the timeout/context is exceeded.
// Returns the result message and whether the command was approved.
func (h *Handler) requestApproval(ctx context.Context, sendEvent func(StreamEvent), args string) (string, bool) {
	var params struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal([]byte(args), &params)

	approvalID := generateApprovalID()
	ch := make(chan bool, 1)
	h.pendingApprovals.Store(approvalID, ch)
	defer h.pendingApprovals.Delete(approvalID)

	sendEvent(StreamEvent{ApprovalRequired: &ApprovalRequest{
		ID:      approvalID,
		Command: params.Command,
	}})

	select {
	case approved := <-ch:
		if !approved {
			return "Command denied by user. The user rejected executing this sudo command.", false
		}
		return "", true
	case <-ctx.Done():
		return "Command approval timed out — the request was cancelled.", false
	case <-time.After(approvalTimeout):
		return "Command approval timed out after 60 seconds.", false
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

	val, ok := h.pendingApprovals.LoadAndDelete(req.ID)
	if !ok {
		writeError(w, http.StatusNotFound, "no pending approval with this ID")
		return
	}

	ch := val.(chan bool)
	ch <- req.Approved

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
