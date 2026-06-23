package moonshot

import (
	"net/http"
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
		ExpectedProviderID:   "moonshot",
		ExpectedProviderName: "Moonshot AI",
		ExpectedProtocol:     aiprovider.ProtocolOpenAIChat,
		ExpectedModelIDs:     []string{"kimi-k2.7-code", "kimi-k2.6"},
		Request: aiprovider.StreamRequest{
			Model: "kimi-k2.6",
			Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
		},
	})
}

func modelByID(t *testing.T, id string) domain.Model {
	t.Helper()
	model, ok := domain.ModelByID(NewAdapter().Models(), id)
	if !ok {
		t.Fatalf("model %q not found", id)
	}
	return model
}

func TestBuildChatRequest_PreservesReasoningContentWhenThinkingEnabled(t *testing.T) {
	model := modelByID(t, "kimi-k2.6")
	req := aiprovider.StreamRequest{
		Model:    "kimi-k2.6",
		Thinking: &domain.ThinkingConfig{Enabled: boolPtr(true)},
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleUser, "Inspect the repo"),
			{
				Role: domain.RoleAssistant,
				Parts: []domain.Part{
					{Kind: domain.PartThinking, Thinking: &domain.ThinkingPart{Text: "Need to inspect README before editing."}},
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
			domain.NewToolResultTurn("call_1", "read_file", "# Devpad", false),
		},
	}

	body := buildChatRequest(req, model)

	if len(body.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(body.Messages))
	}
	if body.Thinking == nil {
		t.Fatal("expected thinking configuration")
	}
	if body.Thinking.Type != "enabled" {
		t.Fatalf("expected thinking enabled, got %q", body.Thinking.Type)
	}
	if body.Thinking.Keep != "all" {
		t.Fatalf("expected keep=all, got %q", body.Thinking.Keep)
	}
	if body.Messages[0].ReasoningContent != nil {
		t.Fatal("expected user message to omit reasoning_content")
	}
	if body.Messages[1].ReasoningContent == nil {
		t.Fatal("expected assistant tool-call message to include reasoning_content")
	}
	if *body.Messages[1].ReasoningContent != "Need to inspect README before editing." {
		t.Fatalf("expected preserved reasoning_content, got %q", *body.Messages[1].ReasoningContent)
	}
	if body.Messages[2].ReasoningContent != nil {
		t.Fatal("expected tool result message to omit reasoning_content")
	}
}

func TestBuildChatRequest_DisablesThinkingWhenRequested(t *testing.T) {
	model := modelByID(t, "kimi-k2.6")
	req := aiprovider.StreamRequest{
		Model:    "kimi-k2.6",
		Thinking: &domain.ThinkingConfig{Enabled: boolPtr(false)},
		Turns:    []domain.Turn{domain.NewTextTurn(domain.RoleUser, "Hello")},
	}

	body := buildChatRequest(req, model)

	if body.Thinking == nil {
		t.Fatal("expected thinking configuration")
	}
	if body.Thinking.Type != "disabled" {
		t.Fatalf("expected thinking disabled, got %q", body.Thinking.Type)
	}
	if body.Thinking.Keep != "" {
		t.Fatalf("expected keep to be omitted when thinking is disabled, got %q", body.Thinking.Keep)
	}
}

// kimi-k2.7-code requires thinking; it must never emit a "disabled" config even
// if a caller asks to turn thinking off.
func TestBuildChatRequest_K27CodeKeepsThinkingEnabled(t *testing.T) {
	model := modelByID(t, "kimi-k2.7-code")
	req := aiprovider.StreamRequest{
		Model:    "kimi-k2.7-code",
		Thinking: &domain.ThinkingConfig{Enabled: boolPtr(false)},
		Turns:    []domain.Turn{domain.NewTextTurn(domain.RoleUser, "Hello")},
	}

	body := buildChatRequest(req, model)

	if body.Thinking != nil && body.Thinking.Type == "disabled" {
		t.Fatal("kimi-k2.7-code must not send a disabled thinking config")
	}
}

func boolPtr(v bool) *bool { return &v }
