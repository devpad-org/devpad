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

// minimaxProvider implements Provider for the MiniMax AI API.
type minimaxProvider struct {
	client  *http.Client
	baseURL string
}

// NewMiniMaxProvider creates a new MiniMax AI provider.
func NewMiniMaxProvider() Provider {
	return &minimaxProvider{
		client:  &http.Client{Timeout: 10 * time.Minute},
		baseURL: "https://api.minimaxi.chat/v1",
	}
}

func (p *minimaxProvider) ID() string { return "minimax" }

func (p *minimaxProvider) Models() []Model {
	return []Model{
		{ID: "MiniMax-M2.7", Name: "MiniMax M2.7", ProviderID: "minimax"},
	}
}

func (p *minimaxProvider) ChatCompletionStream(ctx context.Context, apiKey string, req ChatRequest) (<-chan StreamEvent, error) {
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
		return nil, fmt.Errorf("minimax API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamEvent, 64)
	go readSSEStream(resp.Body, ch)
	return ch, nil
}
