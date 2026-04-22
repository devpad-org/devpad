package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/auth"
)

// mockService stubs the Service interface for handler tests.
type mockService struct {
	findModelFn   func(id string) (Model, error)
	chatStreamFn  func(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error)
}

func (m *mockService) ListModels(_ context.Context) ([]ModelInfo, error)  { return nil, nil }
func (m *mockService) ListProviders(_ context.Context) ([]ProviderInfo, error) { return nil, nil }
func (m *mockService) UpdateProvider(_ context.Context, _ string, _ string, _ bool) error {
	return nil
}
func (m *mockService) FindModel(id string) (Model, error) {
	if m.findModelFn != nil {
		return m.findModelFn(id)
	}
	return Model{ID: id}, nil
}
func (m *mockService) ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error) {
	if m.chatStreamFn != nil {
		return m.chatStreamFn(ctx, req)
	}
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

// mockToolExecutor stubs the ToolExecutor interface for handler tests.
type mockToolExecutor struct {
	executeToolFn func(ctx context.Context, userID, workspaceID int64, name string, args json.RawMessage) (string, error)
}

func (m *mockToolExecutor) ExecuteTool(ctx context.Context, userID, workspaceID int64, name string, args json.RawMessage) (string, error) {
	if m.executeToolFn != nil {
		return m.executeToolFn(ctx, userID, workspaceID, name, args)
	}
	return "ok", nil
}

// mockConversationService stubs the ConversationService interface for handler tests.
type mockConversationService struct{}

func (m *mockConversationService) CreateConversation(_ context.Context, _, _ int64, _ string) (*Conversation, error) {
	return &Conversation{ID: 1}, nil
}
func (m *mockConversationService) ListConversations(_ context.Context, _, _ int64) ([]Conversation, error) {
	return nil, nil
}
func (m *mockConversationService) GetConversation(_ context.Context, _, _ int64) (*Conversation, error) {
	return nil, nil
}
func (m *mockConversationService) DeleteConversation(_ context.Context, _, _ int64) error { return nil }
func (m *mockConversationService) SaveMessages(_ context.Context, _, _ int64, _ []Message) error {
	return nil
}
func (m *mockConversationService) GetMessages(_ context.Context, _, _ int64) ([]Message, error) {
	return nil, nil
}

// parseSSEEvents reads all SSE events from a response body and returns them as StreamEvents.
func parseSSEEvents(t *testing.T, body string) []StreamEvent {
	t.Helper()
	var events []StreamEvent
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var ev StreamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			t.Fatalf("failed to parse SSE event %q: %v", data, err)
		}
		events = append(events, ev)
	}
	return events
}

// agentRequest posts a ChatRequest to the handler and returns the raw SSE body.
func agentRequest(t *testing.T, h *Handler, req ChatRequest) string {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/ai/agent", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	// Inject an authenticated user into the context.
	ctx := context.WithValue(r.Context(), auth.UserContextKey, &auth.User{ID: 1})
	r = r.WithContext(ctx)

	w := httptest.NewRecorder()
	h.HandleAgentChat(w, r)
	return w.Body.String()
}

// TestHandleAgentChat_MultiToolCallOrdering verifies that when the LLM produces two
// tool calls in one iteration, the handler emits a single ToolCalls event containing
// both calls before any ToolResult events. This is the contract the frontend relies on
// to correctly group tools into one assistant message.
func TestHandleAgentChat_MultiToolCallOrdering(t *testing.T) {
	// The mock provider returns two tool calls in the first iteration, then a final
	// text response in the second iteration (no tool calls → loop exits).
	callCount := 0
	svc := &mockService{
		chatStreamFn: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, error) {
			ch := make(chan StreamEvent, 4)
			callCount++
			if callCount == 1 {
				// First iteration: assistant calls two tools.
				ch <- StreamEvent{ToolCalls: []ToolCall{
					{ID: "tc1", Type: "function", Function: ToolCallFunction{Name: "read_file", Arguments: `{"path":"a.txt"}`}},
					{ID: "tc2", Type: "function", Function: ToolCallFunction{Name: "read_file", Arguments: `{"path":"b.txt"}`}},
				}}
				ch <- StreamEvent{Done: true}
			} else {
				// Second iteration: final text response, no tool calls.
				ch <- StreamEvent{Content: "done"}
				ch <- StreamEvent{Done: true}
			}
			close(ch)
			return ch, nil
		},
	}

	executor := &mockToolExecutor{
		executeToolFn: func(_ context.Context, _, _ int64, name string, args json.RawMessage) (string, error) {
			return "result-" + name, nil
		},
	}

	h := NewHandler(svc, &mockConversationService{}, executor)
	body := agentRequest(t, h, ChatRequest{
		Model:       "test-model",
		WorkspaceID: 1,
		Messages:    []Message{{Role: "user", Content: "go"}},
	})

	events := parseSSEEvents(t, body)

	// Find the ToolCalls event(s) and ToolResult events.
	var toolCallEvents []StreamEvent
	var toolResultEvents []StreamEvent
	var doneIdx int
	for i, ev := range events {
		if len(ev.ToolCalls) > 0 {
			toolCallEvents = append(toolCallEvents, ev)
		}
		if ev.ToolResult != nil {
			toolResultEvents = append(toolResultEvents, ev)
		}
		if ev.Done {
			doneIdx = i
		}
	}

	// Exactly one ToolCalls event with both tool calls.
	if len(toolCallEvents) != 1 {
		t.Fatalf("expected 1 ToolCalls event, got %d", len(toolCallEvents))
	}
	if len(toolCallEvents[0].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls in the single ToolCalls event, got %d", len(toolCallEvents[0].ToolCalls))
	}

	// Two ToolResult events (one per tool).
	if len(toolResultEvents) != 2 {
		t.Fatalf("expected 2 ToolResult events, got %d", len(toolResultEvents))
	}

	// ToolCalls event must precede all ToolResult events.
	toolCallIdx := -1
	for i, ev := range events {
		if len(ev.ToolCalls) > 0 {
			toolCallIdx = i
			break
		}
	}
	firstResultIdx := -1
	for i, ev := range events {
		if ev.ToolResult != nil {
			firstResultIdx = i
			break
		}
	}
	if toolCallIdx == -1 || firstResultIdx == -1 {
		t.Fatal("missing ToolCalls or ToolResult events")
	}
	if toolCallIdx >= firstResultIdx {
		t.Errorf("ToolCalls event (index %d) must come before first ToolResult (index %d)", toolCallIdx, firstResultIdx)
	}

	// Done event is last.
	if doneIdx != len(events)-1 {
		t.Errorf("Done event should be last (index %d), got %d events total", doneIdx, len(events))
	}
}
