package mistral

import (
	"reflect"
	"testing"

	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/testkit"
)

func TestBuildChatRequest_SendsImages(t *testing.T) {
	model := NewAdapter().Models()[0]
	body := testkit.JSON(t, buildChatRequest(aiprovider.StreamRequest{Model: model.ID, Turns: testkit.ImageTurns()}, model))
	messages := body.(map[string]any)["messages"].([]any)

	want := []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image_url", "image_url": "data:image/png;base64," + testkit.AttachedImageBase64},
			map[string]any{"type": "text", "text": "What is this?"},
		}},
		messages[1], // the assistant's tool call, unchanged
		map[string]any{"role": "tool", "tool_call_id": "call-1", "content": "Image a.png (1×1) is attached."},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "The images returned by read_file:"},
			map[string]any{"type": "image_url", "image_url": "data:image/jpeg;base64," + testkit.ToolImageBase64},
		}},
		map[string]any{"role": "user", "content": "Thanks"},
	}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("messages = %#v\nwant %#v", messages, want)
	}
}
