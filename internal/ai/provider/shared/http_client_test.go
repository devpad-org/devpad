package shared

import "testing"

func TestNewStreamingClientHasNoTotalTimeout(t *testing.T) {
	client := NewStreamingClient()

	if client.Timeout != 0 {
		t.Fatalf("expected no total timeout for streaming client, got %v", client.Timeout)
	}
	if client.Transport == nil {
		t.Fatal("expected streaming client to configure a transport")
	}
}
