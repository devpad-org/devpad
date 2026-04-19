package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
		return nil, fmt.Errorf("mistral API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamEvent, 64)
	go readSSEStream(resp.Body, ch)
	return ch, nil
}

// readSSEStream reads an OpenAI-compatible SSE stream and sends events to the channel.
// Shared by all providers that use the OpenAI chat completion format.
// Handles both content streaming and tool call accumulation.
func readSSEStream(body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // Allow up to 1MB lines for large tool call args

	// Accumulate tool calls across streaming chunks
	var toolCalls []ToolCall
	toolCallArgs := make(map[int]*strings.Builder)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Emit accumulated tool calls if any
			if len(toolCalls) > 0 {
				for i, tc := range toolCalls {
					if b, ok := toolCallArgs[i]; ok {
						tc.Function.Arguments = b.String()
						toolCalls[i] = tc
					}
				}
				ch <- StreamEvent{ToolCalls: toolCalls}
			}
			ch <- StreamEvent{Done: true}
			return
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]

		// Forward content chunks immediately
		if choice.Delta.Content != "" {
			ch <- StreamEvent{Content: choice.Delta.Content}
		}

		// Accumulate tool calls across chunks
		for _, tc := range choice.Delta.ToolCalls {
			idx := tc.Index
			if tc.ID != "" {
				// New tool call starting
				for len(toolCalls) <= idx {
					toolCalls = append(toolCalls, ToolCall{})
				}
				tcType := tc.Type
				if tcType == "" {
					tcType = "function"
				}
				toolCalls[idx] = ToolCall{
					ID:   tc.ID,
					Type: tcType,
					Function: ToolCallFunction{
						Name: tc.Function.Name,
					},
				}
				toolCallArgs[idx] = &strings.Builder{}
			}
			if tc.Function.Arguments != "" {
				if _, ok := toolCallArgs[idx]; !ok {
					toolCallArgs[idx] = &strings.Builder{}
				}
				toolCallArgs[idx].WriteString(tc.Function.Arguments)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("error reading SSE stream: %v", err)
		ch <- StreamEvent{Error: "error reading response stream"}
	}

	// Emit any accumulated tool calls even if we didn't get [DONE]
	if len(toolCalls) > 0 {
		for i, tc := range toolCalls {
			if b, ok := toolCallArgs[i]; ok {
				tc.Function.Arguments = b.String()
				toolCalls[i] = tc
			}
		}
		ch <- StreamEvent{ToolCalls: toolCalls}
	}
	ch <- StreamEvent{Done: true}
}
