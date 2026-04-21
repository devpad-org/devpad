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

// moonshotProvider implements Provider for the Kimi API.
type moonshotProvider struct {
	client  *http.Client
	baseURL string
}

// NewMoonshotProvider creates a new Kimi provider backed by Moonshot AI.
func NewMoonshotProvider() Provider {
	return &moonshotProvider{
		client:  &http.Client{Timeout: 10 * time.Minute},
		baseURL: "https://api.moonshot.ai/v1",
	}
}

func (p *moonshotProvider) ID() string { return "moonshot" }

func (p *moonshotProvider) Models() []Model {
	return []Model{
		{ID: "kimi-k2.6", Name: "Kimi K2.6", ProviderID: "moonshot"},
	}
}

func (p *moonshotProvider) ChatCompletionStream(ctx context.Context, apiKey string, req ChatRequest) (<-chan StreamEvent, error) {
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
		return nil, fmt.Errorf("moonshot API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamEvent, 64)
	go readSSEStream(resp.Body, ch)
	return ch, nil
}
