package anthropic

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

// interleavedStream is a response whose blocks alternate between thinking,
// text, and tool calls, as Claude 5.5 models produce with progress updates.
const interleavedStream = `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-a"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"I'll check "}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"both files."}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"thinking"}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"thinking_delta","thinking":"Reading a.go first."}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"signature_delta","signature":"sig-b"}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: content_block_start
data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_a","name":"read_file"}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.go\",\"limit\":10}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":3}

event: content_block_start
data: {"type":"content_block_start","index":4,"content_block":{"type":"thinking"}}

event: content_block_delta
data: {"type":"content_block_delta","index":4,"delta":{"type":"signature_delta","signature":"sig-c"}}

event: content_block_stop
data: {"type":"content_block_stop","index":4}

event: content_block_start
data: {"type":"content_block_start","index":5,"content_block":{"type":"tool_use","id":"toolu_b","name":"read_file"}}

event: content_block_delta
data: {"type":"content_block_delta","index":5,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"b.go\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":5}

event: message_stop
data: {"type":"message_stop"}
`

// decodeTurn runs a stream through the decoder and assembles the assistant
// turn the way the orchestrator does before sending it back.
func decodeTurn(t *testing.T, stream string) domain.Turn {
	t.Helper()

	ch := make(chan domain.ProviderEvent, 64)
	go readAnthropicStream(io.NopCloser(strings.NewReader(stream)), ch)

	var text, reasoning strings.Builder
	var state json.RawMessage
	var toolCalls []domain.ToolCall
	for event := range ch {
		if event.Err != nil {
			t.Fatalf("unexpected stream error: %v", event.Err)
		}
		text.WriteString(event.TextDelta)
		reasoning.WriteString(event.ReasoningDelta)
		if len(event.ReasoningState) > 0 {
			state = event.ReasoningState
		}
		if len(event.ToolCalls) > 0 {
			toolCalls = event.ToolCalls
		}
	}

	turn := domain.Turn{Role: domain.RoleAssistant}
	if len(state) > 0 {
		turn.Parts = append(turn.Parts, domain.Part{
			Kind:     domain.PartThinking,
			Thinking: &domain.ThinkingPart{Text: reasoning.String(), State: state},
		})
	}
	if text.Len() > 0 {
		turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartText, Text: text.String()})
	}
	for _, call := range toolCalls {
		call := call
		turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartToolCall, ToolCall: &call})
	}
	return turn
}

// replayedContent builds a follow-up request containing the turn and returns
// the assistant message's content as it goes over the wire.
func replayedContent(t *testing.T, turn domain.Turn) []map[string]any {
	t.Helper()

	req := aiprovider.StreamRequest{
		Model: "claude-opus-5-5",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "Check a.go and b.go"), turn},
	}
	body := buildAnthropicRequest(req, mustAnthropicModel(t, req.Model))

	raw, err := json.Marshal(body["messages"])
	if err != nil {
		t.Fatalf("marshalling messages: %v", err)
	}
	var messages []struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatalf("unmarshalling messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	var content []map[string]any
	if err := json.Unmarshal(messages[1].Content, &content); err != nil {
		t.Fatalf("unmarshalling assistant content: %v", err)
	}
	return content
}

func blockSummary(content []map[string]any) []string {
	summary := make([]string, 0, len(content))
	for _, block := range content {
		switch block["type"] {
		case "thinking":
			summary = append(summary, "thinking:"+block["signature"].(string))
		case "text":
			summary = append(summary, "text:"+block["text"].(string))
		case "tool_use":
			summary = append(summary, "tool_use:"+block["id"].(string))
		default:
			summary = append(summary, block["type"].(string))
		}
	}
	return summary
}

func TestLayout_ReplaysBlocksInOriginalOrder(t *testing.T) {
	turn := decodeTurn(t, interleavedStream)

	content := replayedContent(t, turn)

	got := strings.Join(blockSummary(content), " | ")
	want := "thinking:sig-a | text:I'll check both files. | thinking:sig-b | tool_use:toolu_a | thinking:sig-c | tool_use:toolu_b"
	if got != want {
		t.Fatalf("unexpected block order\n got: %s\nwant: %s", got, want)
	}
	if content[2]["thinking"] != "Reading a.go first." {
		t.Fatalf("expected thinking text to be preserved, got %v", content[2]["thinking"])
	}
}

func TestLayout_PreservesToolInputKeyOrder(t *testing.T) {
	turn := decodeTurn(t, interleavedStream)

	req := aiprovider.StreamRequest{
		Model: "claude-opus-5-5",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "Check a.go and b.go"), turn},
	}
	body := buildAnthropicRequest(req, mustAnthropicModel(t, req.Model))
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling body: %v", err)
	}

	if !strings.Contains(string(raw), `"input":{"path":"a.go","limit":10}`) {
		t.Fatalf("expected tool input in the model's key order, got %s", raw)
	}
}

func TestLayout_SkipsUnansweredToolCalls(t *testing.T) {
	turn := decodeTurn(t, interleavedStream)
	// A failed stream keeps only the tool calls that got results.
	var parts []domain.Part
	for _, part := range turn.Parts {
		if part.Kind == domain.PartToolCall && part.ToolCall.ID == "toolu_b" {
			continue
		}
		parts = append(parts, part)
	}
	turn.Parts = parts

	got := strings.Join(blockSummary(replayedContent(t, turn)), " | ")
	want := "thinking:sig-a | text:I'll check both files. | thinking:sig-b | tool_use:toolu_a | thinking:sig-c"
	if got != want {
		t.Fatalf("unexpected block order\n got: %s\nwant: %s", got, want)
	}
}

func TestLayout_MismatchedTextFallsBackToPartOrder(t *testing.T) {
	turn := decodeTurn(t, interleavedStream)
	for i := range turn.Parts {
		if turn.Parts[i].Kind == domain.PartText {
			turn.Parts[i].Text = "Edited text."
		}
	}

	got := strings.Join(blockSummary(replayedContent(t, turn)), " | ")
	want := "thinking:sig-a | thinking:sig-b | thinking:sig-c | text:Edited text. | tool_use:toolu_a | tool_use:toolu_b"
	if got != want {
		t.Fatalf("unexpected block order\n got: %s\nwant: %s", got, want)
	}
}

func TestLayout_LegacyStateReplaysThinkingFirst(t *testing.T) {
	// Turns saved before the layout existed hold only thinking blocks.
	turn := domain.Turn{
		Role: domain.RoleAssistant,
		Parts: []domain.Part{
			{Kind: domain.PartThinking, Thinking: &domain.ThinkingPart{
				State: json.RawMessage(`[{"type":"thinking","thinking":"","signature":"sig-old"}]`),
			}},
			{Kind: domain.PartText, Text: "Looking."},
			{Kind: domain.PartToolCall, ToolCall: &domain.ToolCall{
				ID:       "toolu_old",
				Type:     "function",
				Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"a.go"}`},
			}},
		},
	}

	got := strings.Join(blockSummary(replayedContent(t, turn)), " | ")
	want := "thinking:sig-old | text:Looking. | tool_use:toolu_old"
	if got != want {
		t.Fatalf("unexpected block order\n got: %s\nwant: %s", got, want)
	}
}

func TestLayout_NotSavedWithoutThinking(t *testing.T) {
	stream := `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_stop
data: {"type":"message_stop"}
`
	turn := decodeTurn(t, stream)

	if state := turn.ThinkingState(); len(state) > 0 {
		t.Fatalf("expected no reasoning state without thinking blocks, got %s", state)
	}
}
