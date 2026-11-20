package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

// Live tests call the real Messages API and cost a few cents per run. They are
// skipped unless DEVPAD_LIVE_TESTS=1 and DEVPAD_TEST_ANTHROPIC_KEY are set.

const liveSystemPrompt = "You are a coding agent working in a Go repository. Use list_dir and " +
	"read_file to explore it. When you need several files, request them in parallel in a " +
	"single response, and tell the user briefly why you want each one as you go."

var liveTools = []domain.ToolDefinition{
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        "read_file",
			Description: "Read a file from the repository. Paths are relative to the repository root.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		},
	},
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        "list_dir",
			Description: "List a directory in the repository. Paths are relative to the repository root.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		},
	},
}

func liveCredentials(t *testing.T) aiprovider.Credentials {
	t.Helper()
	if os.Getenv("DEVPAD_LIVE_TESTS") != "1" {
		t.Skip("set DEVPAD_LIVE_TESTS=1 to run live API tests")
	}
	key := os.Getenv("DEVPAD_TEST_ANTHROPIC_KEY")
	if key == "" {
		t.Skip("set DEVPAD_TEST_ANTHROPIC_KEY to run live Anthropic tests")
	}
	return aiprovider.Credentials{APIKey: key}
}

// liveTurn streams one response and assembles the assistant turn the way the
// orchestrator does. It returns the stream error instead of failing so callers
// can report which request in a conversation was rejected.
func liveTurn(creds aiprovider.Credentials, req aiprovider.StreamRequest) (domain.Turn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	stream, err := NewAdapter().Stream(ctx, creds, req)
	if err != nil {
		return domain.Turn{}, err
	}

	var text, reasoning strings.Builder
	var state json.RawMessage
	var toolCalls []domain.ToolCall
	var streamErr error
	for event := range stream {
		if event.Err != nil {
			streamErr = event.Err
		}
		text.WriteString(event.TextDelta)
		reasoning.WriteString(event.ReasoningDelta)
		if len(event.ReasoningState) > 0 {
			state = event.ReasoningState
		}
		if len(event.ToolCalls) > 0 {
			toolCalls = event.ToolCalls
		}
	}
	if streamErr != nil {
		return domain.Turn{}, streamErr
	}

	turn := domain.Turn{Role: domain.RoleAssistant}
	if reasoning.Len() > 0 || len(state) > 0 {
		turn.Parts = append(turn.Parts, domain.Part{
			Kind:     domain.PartThinking,
			Thinking: &domain.ThinkingPart{Text: reasoning.String(), State: state},
		})
	}
	if text.Len() > 0 {
		turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartText, Text: text.String()})
	}
	for _, call := range toolCalls {
		call := call
		turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartToolCall, ToolCall: &call})
	}
	return turn, nil
}

// layoutSummary lists the blocks the model produced, in order.
func layoutSummary(turn domain.Turn) string {
	var blocks []map[string]any
	if err := json.Unmarshal(turn.ThinkingState(), &blocks); err != nil {
		var kinds []string
		for _, part := range turn.Parts {
			kinds = append(kinds, string(part.Kind))
		}
		return "no thinking: " + strings.Join(kinds, " | ")
	}
	types := make([]string, 0, len(blocks))
	for _, block := range blocks {
		types = append(types, strings.TrimPrefix(strings.TrimSuffix(block["type"].(string), "_ref"), "devpad_"))
	}
	return strings.Join(types, " | ")
}

// runLiveTool serves read_file and list_dir from this repository.
func runLiveTool(t *testing.T, call domain.ToolCall) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")

	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return "Error: invalid arguments"
	}
	path := filepath.Join(root, filepath.Clean("/"+args.Path))
	switch call.Function.Name {
	case "read_file":
		data, err := os.ReadFile(path)
		if err != nil {
			return "Error: " + err.Error()
		}
		if len(data) > 4000 {
			data = append(data[:4000], "\n... (truncated)"...)
		}
		return string(data)
	case "list_dir":
		entries, err := os.ReadDir(path)
		if err != nil {
			return "Error: " + err.Error()
		}
		var names []string
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() {
				name += "/"
			}
			names = append(names, name)
		}
		return strings.Join(names, "\n")
	default:
		return "Error: unknown tool " + call.Function.Name
	}
}

// TestLive_AgentConversationReplays runs a two-message agent conversation the
// way devpad does, replaying every earlier turn, and fails if any request in it
// is rejected.
func TestLive_AgentConversationReplays(t *testing.T) {
	creds := liveCredentials(t)
	modelID := os.Getenv("DEVPAD_LIVE_MODEL")
	if modelID == "" {
		modelID = "claude-sonnet-5-5"
	}

	turns := []domain.Turn{domain.NewTextTurn(domain.RoleSystem, liveSystemPrompt)}
	messages := []string{
		"How is this project built? Look at the Makefile, go.mod, and main.go.",
		"Now explain how the AI provider adapters under internal/ai/provider are wired together.",
	}
	const maxSteps = 8
	for m, message := range messages {
		turns = append(turns, domain.NewTextTurn(domain.RoleUser, message))
		for step := 1; step <= maxSteps; step++ {
			req := aiprovider.StreamRequest{Model: modelID, Turns: turns, Tools: liveTools}
			assistant, err := liveTurn(creds, req)
			if err != nil {
				t.Fatalf("message %d, step %d rejected: %v", m+1, step, err)
			}
			calls := assistant.ToolCalls()
			t.Logf("message %d, step %d: %s", m+1, step, layoutSummary(assistant))
			if len(calls) == 0 {
				turns = append(turns, assistant)
				break
			}
			turns = append(turns, assistant)
			for _, call := range calls {
				turns = append(turns, domain.NewToolResultTurn(call.ID, call.Function.Name, runLiveTool(t, call), false))
			}
			if step == maxSteps {
				t.Logf("message %d stopped after %d steps", m+1, maxSteps)
			}
		}
	}
	fmt.Fprintf(os.Stderr, "conversation completed with %d turns\n", len(turns))
}
