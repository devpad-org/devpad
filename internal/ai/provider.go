package ai

import (
	"context"
)

// Provider is the interface that all AI chat providers must implement.
type Provider interface {
	// ID returns the unique identifier for this provider (e.g. "mistral", "minimax").
	ID() string

	// Models returns the list of models available from this provider.
	Models() []Model

	// ChatCompletionStream sends a chat completion request and streams the response.
	ChatCompletionStream(ctx context.Context, apiKey string, req ChatRequest) (<-chan StreamEvent, error)
}

// Model describes an AI model offered by a provider.
type Model struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ProviderID string `json:"providerId"`
}

// Message is a single message in a chat conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the input for a chat completion.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// StreamEvent represents a single SSE chunk from a streaming chat completion.
type StreamEvent struct {
	Content string `json:"content,omitempty"`
	Done    bool   `json:"done,omitempty"`
	Error   string `json:"error,omitempty"`
}
