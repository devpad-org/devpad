package minimax

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/testkit"
)

func TestAdapterContract(t *testing.T) {
	testkit.RunAdapterContract(t, testkit.AdapterContract{
		NewAdapter: func() aiprovider.Adapter {
			return NewAdapter()
		},
		Configure: func(adapter aiprovider.Adapter, serverURL string, client *http.Client) {
			tested := adapter.(*Adapter)
			tested.baseURL = serverURL
			tested.client = client
		},
		ExpectedProviderID:   "minimax",
		ExpectedProviderName: "MiniMax",
		ExpectedProtocol:     aiprovider.ProtocolOpenAIChat,
		ExpectedModelIDs:     []string{"MiniMax-M3", "MiniMax-M2.7"},
		Request: aiprovider.StreamRequest{
			Model: "MiniMax-M3",
			Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
		},
	})
}

func TestBuildChatRequest_UsesReasoningSplitAndReasoningDetails(t *testing.T) {
	thinkingState := json.RawMessage(`[
		{
			"type":"reasoning.text",
			"id":"reasoning-text-9",
			"format":"MiniMax-response-v1",
			"index":0,
			"text":"I should inspect the current files before editing."
		}
	]`)

	model, ok := domain.ModelByID(NewAdapter().Models(), "MiniMax-M2.7")
	if !ok {
		t.Fatal("expected MiniMax-M2.7 model")
	}

	req := aiprovider.StreamRequest{
		Model: "MiniMax-M2.7",
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleUser, "Check the repo."),
			{
				Role: domain.RoleAssistant,
				Parts: []domain.Part{
					{Kind: domain.PartThinking, Thinking: &domain.ThinkingPart{Text: "I should inspect the current files before editing.", State: thinkingState}},
					{Kind: domain.PartToolCall, ToolCall: &domain.ToolCall{
						ID:   "call_1",
						Type: "function",
						Function: domain.ToolCallFunction{
							Name:      "read_file",
							Arguments: `{"path":"README.md"}`,
						},
					}},
				},
			},
			domain.NewToolResultTurn("call_1", "read_file", "README contents", false),
		},
		Tools: []domain.ToolDefinition{{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "read_file",
				Description: "Read a file",
			},
		}},
	}

	body := buildChatRequest(req, model)

	if !body.ReasoningSplit {
		t.Fatal("expected reasoning_split to be enabled for MiniMax")
	}
	if body.Thinking != nil {
		t.Fatalf("expected MiniMax M2.7 to omit thinking configuration, got %+v", body.Thinking)
	}
	if len(body.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(body.Messages))
	}
	if len(body.Messages[0].ReasoningDetails) != 0 {
		t.Fatal("expected user message to omit reasoning_details")
	}
	if len(body.Messages[1].ReasoningDetails) != 1 {
		t.Fatalf("expected assistant message to include one reasoning detail, got %d", len(body.Messages[1].ReasoningDetails))
	}
	detail := body.Messages[1].ReasoningDetails[0]
	if detail.Type != "reasoning.text" {
		t.Fatalf("expected reasoning detail type reasoning.text, got %q", detail.Type)
	}
	if detail.ID != "reasoning-text-9" {
		t.Fatalf("expected preserved reasoning detail id, got %q", detail.ID)
	}
	if detail.Format != "MiniMax-response-v1" {
		t.Fatalf("expected MiniMax reasoning format, got %q", detail.Format)
	}
	if detail.Text != "I should inspect the current files before editing." {
		t.Fatalf("expected preserved reasoning text, got %q", detail.Text)
	}
	if body.Messages[1].ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("expected tool call name read_file, got %q", body.Messages[1].ToolCalls[0].Function.Name)
	}
}

func TestBuildChatRequest_M3AdaptiveThinking(t *testing.T) {
	model, ok := domain.ModelByID(NewAdapter().Models(), "MiniMax-M3")
	if !ok {
		t.Fatal("expected MiniMax-M3 model")
	}

	body := buildChatRequest(aiprovider.StreamRequest{
		Model: "MiniMax-M3",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
	}, model)

	if !body.ReasoningSplit {
		t.Fatal("expected reasoning_split to be enabled for MiniMax M3")
	}
	if body.Thinking == nil {
		t.Fatal("expected MiniMax M3 thinking configuration")
	}
	if body.Thinking.Type != "adaptive" {
		t.Fatalf("expected adaptive thinking, got %q", body.Thinking.Type)
	}
}

func TestBuildChatRequest_M3DisabledThinking(t *testing.T) {
	model, ok := domain.ModelByID(NewAdapter().Models(), "MiniMax-M3")
	if !ok {
		t.Fatal("expected MiniMax-M3 model")
	}

	body := buildChatRequest(aiprovider.StreamRequest{
		Model:    "MiniMax-M3",
		Turns:    []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
		Thinking: &domain.ThinkingConfig{Enabled: boolPtr(false)},
	}, model)

	if body.ReasoningSplit {
		t.Fatal("expected reasoning_split to be disabled when MiniMax M3 thinking is disabled")
	}
	if body.Thinking == nil {
		t.Fatal("expected MiniMax M3 thinking configuration")
	}
	if body.Thinking.Type != "disabled" {
		t.Fatalf("expected disabled thinking, got %q", body.Thinking.Type)
	}
}

func TestReadSSEStream_ReasoningSplitUsesCumulativeDeltas(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning.text\",\"id\":\"reasoning-text-1\",\"format\":\"MiniMax-response-v1\",\"index\":0,\"text\":\"Plan\"}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning.text\",\"id\":\"reasoning-text-1\",\"format\":\"MiniMax-response-v1\",\"index\":0,\"text\":\"Plan more\"}],\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hello world\"}}]}\n\n" +
		"data: [DONE]\n\n"

	ch := make(chan domain.ProviderEvent, 64)
	go readSSEStream(io.NopCloser(strings.NewReader(sse)), ch)

	var events []domain.ProviderEvent
	for event := range ch {
		events = append(events, event)
	}

	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}
	if events[0].ReasoningDelta != "Plan" {
		t.Fatalf("expected first reasoning chunk Plan, got %q", events[0].ReasoningDelta)
	}
	if string(events[0].ReasoningState) != `[{"type":"reasoning.text","id":"reasoning-text-1","format":"MiniMax-response-v1","index":0,"text":"Plan"}]` {
		t.Fatalf("expected first thinking state payload to be preserved, got %s", string(events[0].ReasoningState))
	}
	if events[1].ReasoningDelta != " more" {
		t.Fatalf("expected second reasoning delta ' more', got %q", events[1].ReasoningDelta)
	}
	if events[2].TextDelta != "Hello" {
		t.Fatalf("expected first content delta Hello, got %q", events[2].TextDelta)
	}
	if events[3].TextDelta != " world" {
		t.Fatalf("expected second content delta ' world', got %q", events[3].TextDelta)
	}
	if !events[4].Done {
		t.Fatal("expected final Done event")
	}
}

func TestReadSSEStream_ToolCallsStillAccumulate(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"a.txt\\\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"

	ch := make(chan domain.ProviderEvent, 64)
	go readSSEStream(io.NopCloser(strings.NewReader(sse)), ch)

	var events []domain.ProviderEvent
	for event := range ch {
		events = append(events, event)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if len(events[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(events[0].ToolCalls))
	}
	if events[0].ToolCalls[0].Function.Arguments != `{"path":"a.txt"}` {
		t.Fatalf("expected accumulated tool arguments, got %q", events[0].ToolCalls[0].Function.Arguments)
	}
	if !events[1].Done {
		t.Fatal("expected final Done event")
	}
}

func boolPtr(v bool) *bool { return &v }
