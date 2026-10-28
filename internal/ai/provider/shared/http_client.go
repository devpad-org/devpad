package shared

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// StreamDecoder consumes a provider response body and emits domain stream events.
type StreamDecoder func(body io.ReadCloser, ch chan<- domain.ProviderEvent)

const (
	streamingResponseHeaderTimeout = 10 * time.Minute

	// Providers reject requests with 429 or 5xx when they are momentarily
	// overloaded, and connections sometimes fail to open. Those failures are
	// transient, so retry before surfacing them as a chat error that costs
	// the user their turn.
	maxStreamAttempts = 4
	maxRetryBackoff   = 30 * time.Second

	// Cloudflare's statuses for an origin that failed or timed out after it
	// was handed the request.
	statusCloudflareUnknown = 520
	statusCloudflareTimeout = 524
)

// initialRetryBackoff is a var so tests can shorten the retry schedule.
var initialRetryBackoff = time.Second

// streamIdleTimeout is how long a stream may wait for data before it is
// abandoned as stalled. It is a var so tests can shorten it.
var streamIdleTimeout = streamingResponseHeaderTimeout

// errStreamStalled reports a provider that stopped sending a stream.
var errStreamStalled = errors.New("provider stream stalled")

// NewStreamingClient creates the default long-lived HTTP client used for streaming APIs.
func NewStreamingClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = streamingResponseHeaderTimeout

	return &http.Client{Transport: transport}
}

// StartStreamRequest posts a JSON body to a streaming endpoint that takes a
// bearer API key and delegates decoding.
func StartStreamRequest(ctx context.Context, client *http.Client, url, apiKey, errorLabel string, payload any, decode StreamDecoder) (<-chan domain.ProviderEvent, error) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+apiKey)
	return StartStreamRequestWithHeaders(ctx, client, url, headers, errorLabel, payload, decode)
}

// StartStreamRequestWithHeaders posts a JSON body to a streaming endpoint with
// the provider's own auth and version headers and delegates decoding.
// Transient provider failures are retried with exponential backoff, but only
// when the provider cannot have started generating: a second attempt at a
// request it is still working on would bill that work twice.
func StartStreamRequestWithHeaders(ctx context.Context, client *http.Client, url string, headers http.Header, errorLabel string, payload any, decode StreamDecoder) (<-chan domain.ProviderEvent, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < maxStreamAttempts; attempt++ {
		attemptCtx, cancel := context.WithCancel(ctx)
		resp, err := sendStreamRequest(attemptCtx, client, url, headers, body)

		var retry bool
		var retryAfter string
		switch {
		case err != nil:
			cancel()
			lastErr = fmt.Errorf("sending request: %w", err)
			retry = ctx.Err() == nil && retryableTransportError(err)
		case resp.StatusCode == http.StatusOK:
			ch := make(chan domain.ProviderEvent, 64)
			go decode(newIdleTimeoutBody(resp.Body, streamIdleTimeout, cancel), ch)
			return ch, nil
		default:
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			cancel()
			lastErr = fmt.Errorf("%s API error (status %d): %s", errorLabel, resp.StatusCode, string(respBody))
			retry = retryableResponse(resp)
			retryAfter = resp.Header.Get("Retry-After")
		}

		if !retry || attempt == maxStreamAttempts-1 {
			return nil, lastErr
		}
		if err := waitForRetry(ctx, retryDelay(attempt, retryAfter)); err != nil {
			return nil, fmt.Errorf("%w (last provider error: %v)", err, lastErr)
		}
	}

	return nil, lastErr
}

func sendStreamRequest(ctx context.Context, client *http.Client, url string, headers http.Header, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header = headers.Clone()
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	return client.Do(httpReq)
}

// retryableTransportError reports whether a request that failed without a
// response is worth sending again. Only one whose connection never opened is:
// a request that was sent and then timed out or lost its connection may still
// be generating. A host that does not exist will not start to.
func retryableTransportError(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return false
	}
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// retryableResponse reports whether a failed provider response is worth
// retrying. A provider that says whether to retry with x-should-retry knows
// best.
func retryableResponse(resp *http.Response) bool {
	switch resp.Header.Get("x-should-retry") {
	case "true":
		return true
	case "false":
		return false
	}
	return retryableStatus(resp.StatusCode)
}

// retryableStatus reports whether a provider status code is worth retrying.
// Gateway errors are not: the gateway gave up waiting on the provider, which
// may still be generating.
func retryableStatus(status int) bool {
	switch status {
	case http.StatusBadGateway, http.StatusGatewayTimeout, statusCloudflareUnknown, statusCloudflareTimeout:
		return false
	}
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

// idleTimeoutBody cancels a streaming request when a read waits too long for
// the provider, which would otherwise block the decoder until the run is
// cancelled. Only time spent waiting in Read counts, so a decoder held up by
// a slow consumer is never taken for a stalled provider.
type idleTimeoutBody struct {
	body    io.ReadCloser
	cancel  context.CancelFunc
	timeout time.Duration
	timer   *time.Timer
	stalled atomic.Bool
}

func newIdleTimeoutBody(body io.ReadCloser, timeout time.Duration, cancel context.CancelFunc) *idleTimeoutBody {
	b := &idleTimeoutBody{body: body, cancel: cancel, timeout: timeout}
	b.timer = time.AfterFunc(timeout, func() {
		b.stalled.Store(true)
		cancel()
	})
	b.timer.Stop()
	return b
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	b.timer.Reset(b.timeout)
	n, err := b.body.Read(p)
	b.timer.Stop()
	if err != nil && b.stalled.Load() {
		return n, fmt.Errorf("%w: no data for %v", errStreamStalled, b.timeout)
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	b.timer.Stop()
	err := b.body.Close()
	b.cancel()
	return err
}
