package anthropic

import (
	"io"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func TestReadAnthropicStream_ReportsFailures(t *testing.T) {
	const start = `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[]}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_a","name":"read_file"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}

`

	tests := []struct {
		name    string
		sse     string
		wantErr string
	}{
		{
			name: "cut off at max_tokens",
			sse: start + `event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"}}

event: message_stop
data: {"type":"message_stop"}
`,
			wantErr: "Anthropic stopped the response because it reached the output token limit",
		},
		{
			name: "context window exceeded",
			sse: start + `event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"model_context_window_exceeded"}}

event: message_stop
data: {"type":"message_stop"}
`,
			wantErr: "Anthropic stopped the response because the conversation reached the model's context limit",
		},
		{
			name:    "stream closes before message_stop",
			sse:     start,
			wantErr: "Anthropic response stream ended before the response finished",
		},
		{
			name: "unreadable event",
			sse: start + `event: content_block_delta
data: {"type":"content_block_delta",
`,
			wantErr: "Anthropic sent a response event that could not be read",
		},
		{
			name: "error event without a message",
			sse: start + `event: error
data: {"type":"error"}
`,
			wantErr: "Anthropic returned an error while streaming the response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := make(chan domain.ProviderEvent, 64)
			go readAnthropicStream(io.NopCloser(strings.NewReader(tt.sse)), ch)
			events := collectEvents(ch)

			last := events[len(events)-1]
			if last.Err == nil || last.Err.Error() != tt.wantErr {
				t.Fatalf("expected final error %q, got %+v", tt.wantErr, last)
			}
			for _, event := range events {
				if event.Done {
					t.Fatal("a failed stream must not report Done")
				}
				if len(event.ToolCalls) > 0 {
					t.Fatal("a failed stream must not emit its tool calls")
				}
			}
		})
	}
}

func TestReadAnthropicStream_ReportsUsageOfATruncatedResponse(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[],"usage":{"input_tokens":100,"output_tokens":1}}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":4096}}

event: message_stop
data: {"type":"message_stop"}
`
	ch := make(chan domain.ProviderEvent, 64)
	go readAnthropicStream(io.NopCloser(strings.NewReader(sse)), ch)
	events := collectEvents(ch)

	if len(events) != 2 || events[0].Usage == nil || events[1].Err == nil {
		t.Fatalf("expected usage then an error, got %+v", events)
	}
	if events[0].Usage.OutputTokens != 4096 {
		t.Fatalf("expected the truncated response's output tokens, got %+v", *events[0].Usage)
	}
}
