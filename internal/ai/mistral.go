package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// mistralProvider implements Provider for the Mistral AI API.
type mistralProvider struct {
	client  *http.Client
	baseURL string
}

// NewMistralProvider creates a new Mistral AI provider.
func NewMistralProvider() Provider {
	return &mistralProvider{
		client:  &http.Client{},
		baseURL: "https://api.mistral.ai/v1",
	}
}

func (p *mistralProvider) ID() string { return "mistral" }

func (p *mistralProvider) Models() []Model {
	return []Model{
		{ID: "devstral-medium-latest", Name: "Devstral", ProviderID: "mistral"},
		{ID: "mistral-large-latest", Name: "Mistral Large", ProviderID: "mistral"},
	}
}

func (p *mistralProvider) ChatCompletionStream(ctx context.Context, apiKey string, req ChatRequest) (<-chan StreamEvent, error) {
	body := map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   true,
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
		return nil, fmt.Errorf("mistral API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamEvent, 64)
	go readSSEStream(resp.Body, ch)
	return ch, nil
}

// readSSEStream reads an OpenAI-compatible SSE stream and sends events to the channel.
// Shared by all providers that use the OpenAI chat completion format.
func readSSEStream(body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			ch <- StreamEvent{Done: true}
			return
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			ch <- StreamEvent{Content: chunk.Choices[0].Delta.Content}
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Error: fmt.Sprintf("reading stream: %v", err)}
	}
	ch <- StreamEvent{Done: true}
}
