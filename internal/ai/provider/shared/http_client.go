package shared

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// StreamDecoder consumes a provider response body and emits domain stream events.
type StreamDecoder func(body io.ReadCloser, ch chan<- domain.ProviderEvent)

const streamingResponseHeaderTimeout = 10 * time.Minute

// NewStreamingClient creates the default long-lived HTTP client used for streaming APIs.
func NewStreamingClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = streamingResponseHeaderTimeout

	return &http.Client{Transport: transport}
}

// StartStreamRequest posts a JSON body to a streaming endpoint and delegates decoding.
func StartStreamRequest(ctx context.Context, client *http.Client, url, apiKey, errorLabel string, payload any, decode StreamDecoder) (<-chan domain.ProviderEvent, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s API error (status %d): %s", errorLabel, resp.StatusCode, string(respBody))
	}

	ch := make(chan domain.ProviderEvent, 64)
	go decode(resp.Body, ch)
	return ch, nil
}
