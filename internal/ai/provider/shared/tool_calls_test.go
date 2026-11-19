package shared

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// toolDelta is one streamed chat completion chunk holding one tool call delta.
func toolDelta(t *testing.T, index int, id, name, arguments string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
		"tool_calls": []any{map[string]any{
			"index":    index,
			"id":       id,
			"function": map[string]any{"name": name, "arguments": arguments},
		}},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	return "data: " + string(raw) + "\n\n"
}

func TestReadSSEStream_ToolCallDeltas(t *testing.T) {
	type want struct{ id, name, arguments string }

	tests := []struct {
		name   string
		deltas func(t *testing.T) []string
		want   []want
	}{
		{
			name: "id repeated on every fragment",
			deltas: func(t *testing.T) []string {
				return []string{
					toolDelta(t, 0, "call_1", "list_files", ""),
					toolDelta(t, 0, "call_1", "", `{"path":"",`),
					toolDelta(t, 0, "call_1", "", `"max_depth":`),
					toolDelta(t, 0, "call_1", "", `2}`),
				}
			},
			want: []want{{"call_1", "list_files", `{"path":"","max_depth":2}`}},
		},
		{
			name: "arguments in one fragment after the name",
			deltas: func(t *testing.T) []string {
				return []string{
					toolDelta(t, 0, "call_1", "list_files", ""),
					toolDelta(t, 0, "call_1", "", `{"path":"frontend"}`),
				}
			},
			want: []want{{"call_1", "list_files", `{"path":"frontend"}`}},
		},
		{
			name: "name repeated on every fragment",
			deltas: func(t *testing.T) []string {
				return []string{
					toolDelta(t, 0, "call_1", "read_file", `{"path":`),
					toolDelta(t, 0, "call_1", "read_file", `"a.txt"}`),
				}
			},
			want: []want{{"call_1", "read_file", `{"path":"a.txt"}`}},
		},
		{
			name: "parallel calls interleaved",
			deltas: func(t *testing.T) []string {
				return []string{
					toolDelta(t, 0, "call_1", "read_file", ""),
					toolDelta(t, 1, "call_2", "list_files", ""),
					toolDelta(t, 0, "call_1", "", `{"path":`),
					toolDelta(t, 1, "call_2", "", `{"path":`),
					toolDelta(t, 0, "call_1", "", `"a.txt"}`),
					toolDelta(t, 1, "call_2", "", `"src"}`),
				}
			},
			want: []want{
				{"call_1", "read_file", `{"path":"a.txt"}`},
				{"call_2", "list_files", `{"path":"src"}`},
			},
		},
		{
			name: "id arrives after the name",
			deltas: func(t *testing.T) []string {
				return []string{
					toolDelta(t, 0, "", "read_file", `{"path":`),
					toolDelta(t, 0, "call_1", "", `"a.txt"}`),
				}
			},
			want: []want{{"call_1", "read_file", `{"path":"a.txt"}`}},
		},
		{
			name: "skipped index leaves no empty call",
			deltas: func(t *testing.T) []string {
				return []string{toolDelta(t, 1, "call_2", "read_file", `{}`)}
			},
			want: []want{{"call_2", "read_file", `{}`}},
		},
		{
			name: "another call at a used index replaces it",
			deltas: func(t *testing.T) []string {
				return []string{
					toolDelta(t, 0, "call_1", "read_file", `{"path":"a.txt"}`),
					toolDelta(t, 0, "call_2", "list_files", `{"path":"src"}`),
				}
			},
			want: []want{{"call_2", "list_files", `{"path":"src"}`}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sse := strings.Join(tt.deltas(t), "") + "data: [DONE]\n\n"
			ch := make(chan domain.ProviderEvent, 16)
			go ReadSSEStream(io.NopCloser(strings.NewReader(sse)), ch)

			var calls []domain.ToolCall
			for event := range ch {
				calls = append(calls, event.ToolCalls...)
			}
			if len(calls) != len(tt.want) {
				t.Fatalf("got %d tool calls, want %d: %+v", len(calls), len(tt.want), calls)
			}
			for i, want := range tt.want {
				got := calls[i]
				if got.ID != want.id || got.Function.Name != want.name || got.Function.Arguments != want.arguments {
					t.Errorf("call %d = %q %q %q, want %q %q %q", i, got.ID, got.Function.Name, got.Function.Arguments, want.id, want.name, want.arguments)
				}
				if got.Type != "function" {
					t.Errorf("call %d type = %q, want function", i, got.Type)
				}
			}
		})
	}
}
