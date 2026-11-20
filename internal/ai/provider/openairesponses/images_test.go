package openairesponses

import (
	"reflect"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/testkit"
)

func TestBuildInput_SendsImages(t *testing.T) {
	input := testkit.JSON(t, buildInput(testkit.ImageTurns()))

	want := []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + testkit.AttachedImageBase64, "detail": "auto"},
			map[string]any{"type": "input_text", "text": "What is this?"},
		}},
		map[string]any{"type": "function_call", "call_id": "call-1", "name": "read_file", "arguments": `{"path":"a.png"}`, "status": "completed"},
		map[string]any{"type": "function_call_output", "call_id": "call-1", "output": []any{
			map[string]any{"type": "input_text", "text": "Image a.png (1×1) is attached."},
			map[string]any{"type": "input_image", "image_url": "data:image/jpeg;base64," + testkit.ToolImageBase64, "detail": "auto"},
		}},
		map[string]any{"role": "user", "content": "Thanks"},
	}
	if !reflect.DeepEqual(input, want) {
		t.Fatalf("input = %#v\nwant %#v", input, want)
	}
}
