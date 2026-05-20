package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateImageRequest(t *testing.T) {
	tests := []struct {
		name  string
		model Model
		part  Part
		want  error
	}{
		{
			name:  "vision model accepts valid image",
			model: Model{ID: "gpt-5.4", Vision: true},
			part:  Part{Kind: PartImage, Image: &ImagePart{MIMEType: "image/png", Data: "aGk="}},
		},
		{
			name:  "vision model accepts data URL image",
			model: Model{ID: "gpt-5.4", Vision: true},
			part:  Part{Kind: PartImage, Image: &ImagePart{MIMEType: "image/png", Data: "data:image/png;base64,aGk="}},
		},
		{
			name:  "non vision model rejects image",
			model: Model{ID: "mistral-medium-3-5"},
			part:  Part{Kind: PartImage, Image: &ImagePart{MIMEType: "image/png", Data: "aGk="}},
			want:  ErrImagesNotSupported,
		},
		{
			name:  "rejects unsupported mime",
			model: Model{ID: "gpt-5.4", Vision: true},
			part:  Part{Kind: PartImage, Image: &ImagePart{MIMEType: "text/plain", Data: "aGk="}},
			want:  ErrInvalidImage,
		},
		{
			name:  "rejects empty image data",
			model: Model{ID: "gpt-5.4", Vision: true},
			part:  Part{Kind: PartImage, Image: &ImagePart{MIMEType: "image/png", Data: ""}},
			want:  ErrInvalidImage,
		},
		{
			name:  "rejects oversized image",
			model: Model{ID: "gpt-5.4", Vision: true},
			part:  Part{Kind: PartImage, Image: &ImagePart{MIMEType: "image/png", Data: strings.Repeat("a", 7*1024*1024)}},
			want:  ErrImageTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateImageRequest(tt.model, ChatRequest{
				Model: tt.model.ID,
				Turns: []Turn{{Role: RoleUser, Parts: []Part{tt.part}}},
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

func TestNormalizeImageData(t *testing.T) {
	got := NormalizeImageData(" data:image/png;base64,aGk= ")
	if got != "aGk=" {
		t.Fatalf("expected base64 payload, got %q", got)
	}
}
