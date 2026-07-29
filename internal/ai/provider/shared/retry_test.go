package shared

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	retryable := []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable}
	for _, status := range retryable {
		if !retryableStatus(status) {
			t.Errorf("expected status %d to be retryable", status)
		}
	}

	permanent := []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound}
	for _, status := range permanent {
		if retryableStatus(status) {
			t.Errorf("expected status %d to be permanent", status)
		}
	}
}
