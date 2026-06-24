package shared

import (
	"net/http"
	"testing"
)

func TestNewStreamingClientHasNoTotalTimeout(t *testing.T) {
	client := NewStreamingClient()

	if client.Timeout != 0 {
		t.Fatalf("expected no total timeout for streaming client, got %v", client.Timeout)
	}
	if client.Transport == nil {
		t.Fatal("expected streaming client to configure a transport")
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if transport.ResponseHeaderTimeout != streamingResponseHeaderTimeout {
		t.Fatalf("expected response header timeout %v, got %v", streamingResponseHeaderTimeout, transport.ResponseHeaderTimeout)
	}
}
