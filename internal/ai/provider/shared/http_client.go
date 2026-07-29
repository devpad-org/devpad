package shared

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// StreamDecoder consumes a provider response body and emits domain stream events.
type StreamDecoder func(body io.ReadCloser, ch chan<- domain.ProviderEvent)

const (
	streamingResponseHeaderTimeout = 10 * time.Minute

	// Providers reject requests with 429 or 5xx when they are momentarily
	// overloaded. Those failures are transient, so retry before surfacing them
	// as a chat error that costs the user their turn.
	maxStreamAttempts = 4
	maxRetryBackoff   = 30 * time.Second
)

// initialRetryBackoff is a var so tests can shorten the retry schedule.
var initialRetryBackoff = time.Second

// NewStreamingClient creates the default long-lived HTTP client used for streaming APIs.
func NewStreamingClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = streamingResponseHeaderTimeout

	return &http.Client{Transport: transport}
}

// StartStreamRequest posts a JSON body to a streaming endpoint and delegates decoding.
// Transient provider failures are retried with exponential backoff.
func StartStreamRequest(ctx context.Context, client *http.Client, url, apiKey, errorLabel string, payload any, decode StreamDecoder) (<-chan domain.ProviderEvent, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < maxStreamAttempts; attempt++ {
		resp, err := sendStreamRequest(ctx, client, url, apiKey, body)
		if err != nil {
			return nil, fmt.Errorf("sending request: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			ch := make(chan domain.ProviderEvent, 64)
			go decode(resp.Body, ch)
			return ch, nil
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		lastErr = fmt.Errorf("%s API error (status %d): %s", errorLabel, resp.StatusCode, string(respBody))

		if !retryableStatus(resp.StatusCode) || attempt == maxStreamAttempts-1 {
			return nil, lastErr
		}
		if err := waitForRetry(ctx, retryDelay(attempt, resp.Header.Get("Retry-After"))); err != nil {
			return nil, fmt.Errorf("%w (last provider error: %v)", err, lastErr)
		}
	}

	return nil, lastErr
}

func sendStreamRequest(ctx context.Context, client *http.Client, url, apiKey string, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	return client.Do(httpReq)
}

// retryableStatus reports whether a provider status code is worth retrying.
func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

// retryDelay honours the provider's Retry-After hint when it sends one and
// otherwise backs off exponentially with jitter to avoid retry storms.
func retryDelay(attempt int, retryAfter string) time.Duration {
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
		return min(time.Duration(seconds)*time.Second, maxRetryBackoff)
	}
	if at, err := http.ParseTime(retryAfter); err == nil {
		if wait := time.Until(at); wait > 0 {
			return min(wait, maxRetryBackoff)
		}
	}

	backoff := min(initialRetryBackoff<<attempt, maxRetryBackoff)
	jitter := time.Duration(rand.Int63n(int64(backoff / 2)))

	return backoff/2 + jitter
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
