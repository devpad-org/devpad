package shared

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// shortenRetryBackoff keeps retry tests fast without changing the retry schedule shape.
func shortenRetryBackoff(t *testing.T) {
	t.Helper()
	original := initialRetryBackoff
	initialRetryBackoff = time.Millisecond
	t.Cleanup(func() { initialRetryBackoff = original })
}

func discardDecoder(body io.ReadCloser, ch chan<- domain.ProviderEvent) {
	defer body.Close()
	close(ch)
}

func startTestStream(t *testing.T, handler http.HandlerFunc) (<-chan domain.ProviderEvent, error, *int64) {
	t.Helper()

	var calls int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	ch, err := StartStreamRequest(context.Background(), server.Client(), server.URL, "test-key", "testprovider", map[string]string{"model": "test"}, discardDecoder)
	return ch, err, &calls
}

func TestStartStreamRequestRetriesOverloadedProvider(t *testing.T) {
	shortenRetryBackoff(t)

	var attempts int64
	ch, err, calls := startTestStream(t, func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt64(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"type":"engine_overloaded_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	})
	if err != nil {
		t.Fatalf("expected the retried request to succeed, got %v", err)
	}
	if ch == nil {
		t.Fatal("expected an event channel on success")
	}
	if got := atomic.LoadInt64(calls); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestStartStreamRequestSurfacesErrorAfterExhaustingRetries(t *testing.T) {
	shortenRetryBackoff(t)

	_, err, calls := startTestStream(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream failure", http.StatusInternalServerError)
	})
	if err == nil {
		t.Fatal("expected an error once retries are exhausted")
	}
	if !strings.Contains(err.Error(), "testprovider API error (status 500)") {
		t.Fatalf("expected provider and status in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "upstream failure") {
		t.Fatalf("expected upstream body in error, got %v", err)
	}
	if got := atomic.LoadInt64(calls); got != maxStreamAttempts {
		t.Fatalf("expected %d attempts, got %d", maxStreamAttempts, got)
	}
}

func TestStartStreamRequestDoesNotRetryClientErrors(t *testing.T) {
	shortenRetryBackoff(t)

	_, err, calls := startTestStream(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	})
	if err == nil {
		t.Fatal("expected an error for a client error status")
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Fatalf("expected a single attempt for a non-retryable status, got %d", got)
	}
}

func TestStartStreamRequestDoesNotRetryGatewayErrors(t *testing.T) {
	shortenRetryBackoff(t)

	_, err, calls := startTestStream(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream timed out", http.StatusGatewayTimeout)
	})
	if err == nil {
		t.Fatal("expected an error for a gateway timeout")
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Fatalf("expected a single attempt for a gateway timeout, got %d", got)
	}
}

func TestStartStreamRequestFollowsShouldRetryHeader(t *testing.T) {
	shortenRetryBackoff(t)

	_, err, calls := startTestStream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-should-retry", "false")
		http.Error(w, "internal error", http.StatusInternalServerError)
	})
	if err == nil {
		t.Fatal("expected an error for a failed request")
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Fatalf("expected a single attempt when the provider says not to retry, got %d", got)
	}
}

func TestStartStreamRequestDoesNotRetryWhenProviderStallsBeforeHeaders(t *testing.T) {
	shortenRetryBackoff(t)

	release := make(chan struct{})
	var calls int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&calls, 1)
		<-release
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 50 * time.Millisecond
	client.Transport = transport

	_, err := StartStreamRequest(context.Background(), client, server.URL, "test-key", "testprovider", map[string]string{}, discardDecoder)
	if err == nil {
		t.Fatal("expected the stalled request to fail")
	}
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("expected a single attempt for a request the provider received, got %d", got)
	}
}

func TestStartStreamRequestDoesNotRetryDroppedConnections(t *testing.T) {
	shortenRetryBackoff(t)

	_, err, calls := startTestStream(t, func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijacking the connection: %v", err)
			return
		}
		conn.Close()
	})
	if err == nil {
		t.Fatal("expected the dropped connection to fail the request")
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Fatalf("expected a single attempt for a request the provider received, got %d", got)
	}
}

func TestStartStreamRequestRetriesConnectionsThatNeverOpened(t *testing.T) {
	shortenRetryBackoff(t)

	var calls int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	var dials int64
	transport := server.Client().Transport.(*http.Transport).Clone()
	dial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if atomic.AddInt64(&dials, 1) == 1 {
			return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.ECONNREFUSED}
		}
		return dial(ctx, network, addr)
	}

	ch, err := StartStreamRequest(context.Background(), &http.Client{Transport: transport}, server.URL, "test-key", "testprovider", map[string]string{}, discardDecoder)
	if err != nil {
		t.Fatalf("expected the request to succeed once the connection opened, got %v", err)
	}
	if ch == nil {
		t.Fatal("expected an event channel on success")
	}
	if got := atomic.LoadInt64(&dials); got != 2 {
		t.Fatalf("expected 2 dials, got %d", got)
	}
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("expected the provider to receive the request once, got %d", got)
	}
}

func TestStartStreamRequestSendsHeadersOnEveryAttempt(t *testing.T) {
	shortenRetryBackoff(t)

	var auth []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("x-api-key"))
		attempt := len(auth)
		mu.Unlock()
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	headers := http.Header{}
	headers.Set("x-api-key", "test-key")
	if _, err := StartStreamRequestWithHeaders(context.Background(), server.Client(), server.URL, headers, "testprovider", map[string]string{}, discardDecoder); err != nil {
		t.Fatalf("expected the retried request to succeed, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(auth) != 2 || auth[0] != "test-key" || auth[1] != "test-key" {
		t.Fatalf("expected the API key on both attempts, got %q", auth)
	}
	if len(headers) != 1 {
		t.Fatalf("expected the caller's headers to be left alone, got %v", headers)
	}
}

func TestStartStreamRequestAbandonsStalledStream(t *testing.T) {
	original := streamIdleTimeout
	streamIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { streamIdleTimeout = original })

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {}\n\n"))
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	readErr := make(chan error, 1)
	decode := func(body io.ReadCloser, ch chan<- domain.ProviderEvent) {
		defer close(ch)
		defer body.Close()
		_, err := io.ReadAll(body)
		readErr <- err
	}

	if _, err := StartStreamRequest(context.Background(), server.Client(), server.URL, "test-key", "testprovider", map[string]string{}, decode); err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}

	select {
	case err := <-readErr:
		if !errors.Is(err, errStreamStalled) {
			t.Fatalf("expected the stalled stream error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("expected the stalled stream to be abandoned")
	}
}

func TestStartStreamRequestDoesNotTakeSlowConsumerForStall(t *testing.T) {
	original := streamIdleTimeout
	streamIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { streamIdleTimeout = original })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)

	readErr := make(chan error, 1)
	decode := func(body io.ReadCloser, ch chan<- domain.ProviderEvent) {
		defer close(ch)
		defer body.Close()
		time.Sleep(4 * streamIdleTimeout)
		_, err := io.ReadAll(body)
		readErr <- err
	}

	if _, err := StartStreamRequest(context.Background(), server.Client(), server.URL, "test-key", "testprovider", map[string]string{}, decode); err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	if err := <-readErr; err != nil {
		t.Fatalf("expected the delayed read to succeed, got %v", err)
	}
}

func TestStartStreamRequestSurfacesTransportErrorAfterExhaustingRetries(t *testing.T) {
	shortenRetryBackoff(t)

	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()

	_, err := StartStreamRequest(context.Background(), http.DefaultClient, url, "test-key", "testprovider", map[string]string{}, discardDecoder)
	if err == nil {
		t.Fatal("expected an error when the provider is unreachable")
	}
	if !strings.Contains(err.Error(), "sending request") {
		t.Fatalf("expected the transport error, got %v", err)
	}
}

func TestStartStreamRequestStopsRetryingWhenContextIsCancelled(t *testing.T) {
	var calls int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := StartStreamRequest(ctx, server.Client(), server.URL, "test-key", "testprovider", map[string]string{}, discardDecoder)
	if err == nil {
		t.Fatal("expected an error when the context expires during backoff")
	}
	if got := atomic.LoadInt64(&calls); got >= maxStreamAttempts {
		t.Fatalf("expected retries to stop early, got %d attempts", got)
	}
}

func TestRetryDelayHonoursRetryAfterSeconds(t *testing.T) {
	if delay := retryDelay(0, "5"); delay != 5*time.Second {
		t.Fatalf("expected the Retry-After hint to be used, got %v", delay)
	}
	if delay := retryDelay(0, "600"); delay != maxRetryBackoff {
		t.Fatalf("expected Retry-After to be capped at %v, got %v", maxRetryBackoff, delay)
	}
}

func TestRetryDelayBacksOffExponentially(t *testing.T) {
	first := retryDelay(0, "")
	third := retryDelay(2, "")

	if first > initialRetryBackoff {
		t.Fatalf("expected the first delay to stay within %v, got %v", initialRetryBackoff, first)
	}
	if third <= first {
		t.Fatalf("expected later attempts to back off further, got %v then %v", first, third)
	}
	if third > maxRetryBackoff {
		t.Fatalf("expected delays to stay under %v, got %v", maxRetryBackoff, third)
	}
}

func TestRetryableStatus(t *testing.T) {
	retryable := []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable, 529}
	for _, status := range retryable {
		if !retryableStatus(status) {
			t.Errorf("expected status %d to be retryable", status)
		}
	}

	permanent := []int{
		http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusBadGateway, http.StatusGatewayTimeout, statusCloudflareUnknown, statusCloudflareTimeout,
	}
	for _, status := range permanent {
		if retryableStatus(status) {
			t.Errorf("expected status %d not to be retried", status)
		}
	}
}

func TestRetryableResponseFollowsShouldRetryHeader(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		shouldRetry string
		want        bool
	}{
		{name: "provider asks for a retry", status: http.StatusBadGateway, shouldRetry: "true", want: true},
		{name: "provider refuses a retry", status: http.StatusServiceUnavailable, shouldRetry: "false", want: false},
		{name: "no header falls back to the status", status: http.StatusServiceUnavailable, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tt.status, Header: http.Header{}}
			if tt.shouldRetry != "" {
				resp.Header.Set("x-should-retry", tt.shouldRetry)
			}
			if got := retryableResponse(resp); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestRetryableTransportError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "connection refused",
			err:  &url.Error{Op: "Post", URL: "https://api.example.com", Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
			want: true,
		},
		{
			name: "host that does not exist",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "api.example.invalid", IsNotFound: true}},
			want: false,
		},
		{
			name: "connection reset after sending",
			err:  &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET},
			want: false,
		},
		{
			name: "timeout awaiting headers",
			err:  &url.Error{Op: "Post", URL: "https://api.example.com", Err: errors.New("net/http: timeout awaiting response headers")},
			want: false,
		},
		{
			name: "connection closed after sending",
			err:  io.EOF,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryableTransportError(tt.err); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}
