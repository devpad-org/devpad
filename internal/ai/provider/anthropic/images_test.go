package anthropic

import (
	"reflect"
	"testing"

	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/testkit"
)

func TestBuildAnthropicRequest_SendsImages(t *testing.T) {
	req := aiprovider.StreamRequest{Model: "claude-sonnet-5-5", Turns: testkit.ImageTurns()}
	body := testkit.JSON(t, buildAnthropicRequest(req, mustAnthropicModel(t, req.Model)))
	messages := body.(map[string]any)["messages"].([]any)

	want := []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": testkit.AttachedImageBase64}},
			map[string]any{"type": "text", "text": "What is this?"},
		}},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "call-1", "name": "read_file", "input": map[string]any{"path": "a.png"}},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "call-1", "content": []any{
				map[string]any{"type": "text", "text": "Image a.png (1×1) is attached."},
				map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/jpeg", "data": testkit.ToolImageBase64}},
			}},
		}},
		map[string]any{"role": "user", "content": "Thanks"},
	}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("messages = %#v\nwant %#v", messages, want)
	}
}
