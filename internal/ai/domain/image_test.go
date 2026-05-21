package domain

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

const validPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="

func TestValidateImageRequest(t *testing.T) {
	validImage := Part{Kind: PartImage, Image: &ImagePart{MIMEType: "image/png", Data: validPNGBase64}}

	tests := []struct {
		name  string
		model Model
		turns []Turn
		want  error
	}{
		{
			name:  "vision model accepts valid image",
			model: Model{ID: "gpt-5.4", Vision: true},
			turns: []Turn{{
				Role:  RoleUser,
				Parts: []Part{validImage},
			}},
		},
		{
			name:  "vision model accepts data URL image",
			model: Model{ID: "gpt-5.4", Vision: true},
			turns: []Turn{{
				Role:  RoleUser,
				Parts: []Part{{Kind: PartImage, Image: &ImagePart{MIMEType: "image/png", Data: "data:image/png;base64," + validPNGBase64}}},
			}},
		},
		{
			name:  "vision model accepts maximum images per turn",
			model: Model{ID: "gpt-5.4", Vision: true},
			turns: []Turn{{
				Role:  RoleUser,
				Parts: repeatPart(validImage, MaxImagesPerTurn),
			}},
		},
		{
			name:  "vision model rejects too many images in one turn",
			model: Model{ID: "gpt-5.4", Vision: true},
			turns: []Turn{{
				Role:  RoleUser,
				Parts: repeatPart(validImage, MaxImagesPerTurn+1),
			}},
			want: ErrInvalidImage,
		},
		{
			name:  "vision model rejects images on assistant turns",
			model: Model{ID: "gpt-5.4", Vision: true},
			turns: []Turn{{
				Role:  RoleAssistant,
				Parts: []Part{validImage},
			}},
			want: ErrInvalidImage,
		},
		{
			name:  "non vision model rejects image",
			model: Model{ID: "mistral-medium-3-5"},
			turns: []Turn{{
				Role:  RoleUser,
				Parts: []Part{validImage},
			}},
			want: ErrImagesNotSupported,
		},
		{
			name:  "rejects unsupported mime",
			model: Model{ID: "gpt-5.4", Vision: true},
			turns: []Turn{{
				Role:  RoleUser,
				Parts: []Part{{Kind: PartImage, Image: &ImagePart{MIMEType: "text/plain", Data: validPNGBase64}}},
			}},
			want: ErrInvalidImage,
		},
		{
			name:  "rejects malformed data URL",
			model: Model{ID: "gpt-5.4", Vision: true},
			turns: []Turn{{
				Role:  RoleUser,
				Parts: []Part{{Kind: PartImage, Image: &ImagePart{MIMEType: "image/png", Data: "data:text/html,<script>alert(1)</script>"}}},
			}},
			want: ErrInvalidImage,
		},
		{
			name:  "rejects data URL without base64 marker",
			model: Model{ID: "gpt-5.4", Vision: true},
			turns: []Turn{{
				Role:  RoleUser,
				Parts: []Part{{Kind: PartImage, Image: &ImagePart{MIMEType: "image/png", Data: "data:image/png," + validPNGBase64}}},
			}},
			want: ErrInvalidImage,
		},
		{
			name:  "rejects data URL mime mismatch",
			model: Model{ID: "gpt-5.4", Vision: true},
			turns: []Turn{{
				Role:  RoleUser,
				Parts: []Part{{Kind: PartImage, Image: &ImagePart{MIMEType: "image/jpeg", Data: "data:image/png;base64," + validPNGBase64}}},
			}},
			want: ErrInvalidImage,
		},
		{
			name:  "rejects content that does not match mime type",
			model: Model{ID: "gpt-5.4", Vision: true},
			turns: []Turn{{
				Role:  RoleUser,
				Parts: []Part{{Kind: PartImage, Image: &ImagePart{MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("hello"))}}},
			}},
			want: ErrInvalidImage,
		},
		{
			name:  "rejects empty image data",
			model: Model{ID: "gpt-5.4", Vision: true},
			turns: []Turn{{
				Role:  RoleUser,
				Parts: []Part{{Kind: PartImage, Image: &ImagePart{MIMEType: "image/png", Data: ""}}},
			}},
			want: ErrInvalidImage,
		},
		{
			name:  "rejects oversized image",
			model: Model{ID: "gpt-5.4", Vision: true},
			turns: []Turn{{
				Role:  RoleUser,
				Parts: []Part{{Kind: PartImage, Image: &ImagePart{MIMEType: "image/png", Data: strings.Repeat("a", 7*1024*1024)}}},
			}},
			want: ErrImageTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateImageRequest(tt.model, ChatRequest{
				Model: tt.model.ID,
				Turns: tt.turns,
			})
			if tt.want == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}
}

func repeatPart(part Part, count int) []Part {
	parts := make([]Part, count)
	for i := range parts {
		parts[i] = part
	}
	return parts
}

func TestNormalizeImageData(t *testing.T) {
	got := NormalizeImageData(" data:image/png;base64," + validPNGBase64 + " ")
	if got != validPNGBase64 {
		t.Fatalf("expected base64 payload, got %q", got)
	}
}

func TestNormalizeImageDataLeavesMalformedDataURLIntact(t *testing.T) {
	input := "data:text/html,<script>alert(1)</script>"
	if got := NormalizeImageData(input); got != input {
		t.Fatalf("expected malformed data URL to remain intact, got %q", got)
	}
}
