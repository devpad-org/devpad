package testkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

// AdapterContract configures the shared adapter contract suite.
type AdapterContract struct {
	NewAdapter           func() aiprovider.Adapter
	Configure            func(adapter aiprovider.Adapter, serverURL string, client *http.Client)
	ExpectedProviderID   string
	ExpectedProviderName string
	ExpectedProtocol     aiprovider.Protocol
	ExpectedModelIDs     []string
	Request              aiprovider.StreamRequest
}

// RunAdapterContract validates the common provider-adapter behavior expected in Phase 3.
func RunAdapterContract(t *testing.T, contract AdapterContract) {
	t.Helper()

	t.Run("metadata", func(t *testing.T) {
		adapter := contract.NewAdapter()

		if adapter.ProviderID() != contract.ExpectedProviderID {
			t.Fatalf("expected provider ID %q, got %q", contract.ExpectedProviderID, adapter.ProviderID())
		}
		if adapter.ProviderName() != contract.ExpectedProviderName {
			t.Fatalf("expected provider name %q, got %q", contract.ExpectedProviderName, adapter.ProviderName())
		}
		if adapter.Protocol() != contract.ExpectedProtocol {
			t.Fatalf("expected protocol %q, got %q", contract.ExpectedProtocol, adapter.Protocol())
		}

		models := adapter.Models()
		if len(models) != len(contract.ExpectedModelIDs) {
			t.Fatalf("expected %d models, got %d", len(contract.ExpectedModelIDs), len(models))
		}
		for index, modelID := range contract.ExpectedModelIDs {
			if models[index].ID != modelID {
				t.Fatalf("expected model %d to be %q, got %q", index, modelID, models[index].ID)
			}
		}
	})

	t.Run("auth_header_and_stream_completion", func(t *testing.T) {
		var authHeader string
		var acceptHeader string
		var contentType string

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader = r.Header.Get("Authorization")
			acceptHeader = r.Header.Get("Accept")
			contentType = r.Header.Get("Content-Type")
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		}))
		defer server.Close()

		adapter := contract.NewAdapter()
		contract.Configure(adapter, server.URL, server.Client())

		stream, err := adapter.Stream(context.Background(), aiprovider.Credentials{APIKey: "test-key"}, contract.Request)
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}

		events := collectEvents(stream)
		if authHeader != "Bearer test-key" {
			t.Fatalf("expected bearer auth header, got %q", authHeader)
		}
		if acceptHeader != "text/event-stream" {
			t.Fatalf("expected SSE accept header, got %q", acceptHeader)
		}
		if !strings.Contains(contentType, "application/json") {
			t.Fatalf("expected JSON content type, got %q", contentType)
		}
		if len(events) < 2 {
			t.Fatalf("expected at least content and done events, got %d", len(events))
		}
		if events[0].TextDelta != "hello" {
			t.Fatalf("expected first content chunk hello, got %q", events[0].TextDelta)
		}
		if !events[len(events)-1].Done {
			t.Fatalf("expected final done event, got %+v", events[len(events)-1])
		}
	})

	// Uses a client error because 429 and 5xx are retried with backoff; retry
	// behaviour itself is covered in internal/ai/provider/shared.
	t.Run("error_propagation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "upstream failure", http.StatusBadRequest)
		}))
		defer server.Close()

		adapter := contract.NewAdapter()
		contract.Configure(adapter, server.URL, server.Client())

		_, err := adapter.Stream(context.Background(), aiprovider.Credentials{APIKey: "test-key"}, contract.Request)
		if err == nil {
			t.Fatal("expected stream error")
		}
		if !strings.Contains(err.Error(), contract.ExpectedProviderID) {
			t.Fatalf("expected provider ID in error, got %v", err)
		}
		if !strings.Contains(err.Error(), "upstream failure") {
			t.Fatalf("expected upstream body in error, got %v", err)
		}
	})

	t.Run("context_cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		}))
		defer server.Close()

		adapter := contract.NewAdapter()
		contract.Configure(adapter, server.URL, server.Client())

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := adapter.Stream(ctx, aiprovider.Credentials{APIKey: "test-key"}, contract.Request)
		if err == nil {
			t.Fatal("expected cancellation error")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "context canceled") {
			t.Fatalf("expected context cancellation error, got %v", err)
		}
	})
}

func collectEvents(stream <-chan domain.ProviderEvent) []domain.ProviderEvent {
	var events []domain.ProviderEvent
	for event := range stream {
		events = append(events, event)
	}

	return events
}
