package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func bridgeTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{baseURL: srv.URL, http: srv.Client()}
}

// The agent is under the workspace's control, so devpad reads only so much
// of what it sends.
func TestGitBridgeRequestsLimitsWhatItReads(t *testing.T) {
	huge := `{"requests":[{"id":"aa","op":"fetch","branches":["` + strings.Repeat("x", maxGitBridgeResponse) + `"]}]}`
	c := bridgeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("session") != "s1" {
			t.Errorf("poll without its session: %s", r.URL)
		}
		w.Write([]byte(huge))
	})
	if _, err := c.GitBridgeRequests(context.Background(), "s1", time.Second); err == nil {
		t.Fatal("an oversized poll response was accepted")
	}

	c = bridgeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"` + strings.Repeat("y", 2*maxGitBridgeResponse) + `"}`))
	})
	err := c.GitBridgeRespond(context.Background(), "s1", "aa", GitBridgeResult{})
	var agentErr *AgentError
	if !errors.As(err, &agentErr) || len(agentErr.Message) > maxGitBridgeResponse {
		t.Fatalf("an oversized error was read whole: %T", err)
	}

	c = bridgeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/aa/result") || r.URL.Query().Get("session") != "s1" {
			t.Errorf("answer without its session: %s", r.URL)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.GitBridgeRespond(context.Background(), "s1", "aa", GitBridgeResult{}); err != nil {
		t.Fatalf("respond: %v", err)
	}
}
