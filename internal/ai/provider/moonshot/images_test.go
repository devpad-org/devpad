package moonshot

import (
	"reflect"
	"testing"

	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/testkit"
)

func TestBuildChatRequest_SendsImages(t *testing.T) {
	model := modelByID(t, "kimi-k3")
	body := testkit.JSON(t, buildChatRequest(aiprovider.StreamRequest{Model: model.ID, Turns: testkit.ImageTurns()}, model))
	messages := body.(map[string]any)["messages"].([]any)
	if len(messages) != 5 {
		t.Fatalf("got %d messages, want 5: %#v", len(messages), messages)
	}

	wantContent := []any{
		[]any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + testkit.AttachedImageBase64}},
			map[string]any{"type": "text", "text": "What is this?"},
		},
		messages[1].(map[string]any)["content"], // the assistant's tool call, unchanged
		"Image a.png (1×1) is attached.",
		[]any{
			map[string]any{"type": "text", "text": "The images returned by read_file:"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64," + testkit.ToolImageBase64}},
		},
		"Thanks",
	}
	wantRoles := []string{"user", "assistant", "tool", "user", "user"}
	for i, message := range messages {
		message := message.(map[string]any)
		if message["role"] != wantRoles[i] || !reflect.DeepEqual(message["content"], wantContent[i]) {
			t.Fatalf("message %d = %#v\nwant role %q, content %#v", i, message, wantRoles[i], wantContent[i])
		}
	}
}
