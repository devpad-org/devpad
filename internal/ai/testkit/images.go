package testkit

import (
	"encoding/json"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// Base64 of the image data in ImageTurns, as adapters send it.
const (
	AttachedImageBase64 = "b25l" // "one"
	ToolImageBase64     = "dHdv" // "two"
)

// ImageTurns returns a conversation that has an attached image, a tool
// call whose result returned an image, and then a text-only user turn.
func ImageTurns() []domain.Turn {
	return []domain.Turn{
		{Role: domain.RoleUser, Parts: []domain.Part{
			domain.NewImagePart(domain.ImagePart{AttachmentID: 1, MediaType: "image/png", Width: 1, Height: 1, Data: []byte("one")}),
			{Kind: domain.PartText, Text: "What is this?"},
		}},
		{Role: domain.RoleAssistant, Parts: []domain.Part{{
			Kind: domain.PartToolCall,
			ToolCall: &domain.ToolCall{
				ID:       "call-1",
				Type:     "function",
				Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"a.png"}`},
			},
		}}},
		domain.NewToolResultTurnFromPart(domain.ToolResultPart{
			ToolCallID: "call-1",
			Name:       "read_file",
			Content:    "Image a.png (1×1) is attached.",
			Images:     []domain.ImagePart{{AttachmentID: 2, MediaType: "image/jpeg", Width: 1, Height: 1, Data: []byte("two")}},
		}),
		domain.NewTextTurn(domain.RoleUser, "Thanks"),
	}
}

// JSON encodes a request body and decodes it back as generic JSON, so tests
// check what is sent over the wire.
func JSON(t *testing.T, body any) any {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return decoded
}
