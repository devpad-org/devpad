package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// openAIProvider implements Provider for the OpenAI API.
type openAIProvider struct {
	client  *http.Client
	baseURL string
}

// NewOpenAIProvider creates a new OpenAI provider.
func NewOpenAIProvider() Provider {
	return &openAIProvider{
		client:  &http.Client{Timeout: 10 * time.Minute},
		baseURL: "https://api.openai.com/v1",
	}
}

func (p *openAIProvider) ID() string { return "openai" }

func (p *openAIProvider) Models() []Model {
	return []Model{
		{ID: "gpt-5.4", Name: "GPT-5.4", ProviderID: "openai"},
	}
}

func (p *openAIProvider) ChatCompletionStream(ctx context.Context, apiKey string, req ChatRequest) (<-chan StreamEvent, error) {
	body := map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   true,
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamEvent, 64)
	go readSSEStream(resp.Body, ch)
	return ch, nil
}
