package ai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/app"
	"github.com/devpad-org/devpad/internal/ai/approval"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/ai/orchestrator"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/provider/anthropic"
	"github.com/devpad-org/devpad/internal/ai/question"
	"github.com/devpad-org/devpad/internal/ai/storage"
	aitools "github.com/devpad-org/devpad/internal/ai/tools"
	appdb "github.com/devpad-org/devpad/internal/database"
)

// These tests run whole conversations through the real run service,
// orchestrator, and SQLite storage, and check that the history the server
// saves is exactly the history the model is sent on the next request.

// recordingBackend wraps a provider backend and keeps every request's turns.
type recordingBackend struct {
	orchestrator.ChatService
	mu       sync.Mutex
	requests [][]domain.Turn
}

func (b *recordingBackend) ChatStream(ctx context.Context, req domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
	b.mu.Lock()
	b.requests = append(b.requests, withoutSystemTurns(req.Turns))
	b.mu.Unlock()
	return b.ChatService.ChatStream(ctx, req)
}

func (b *recordingBackend) request(i int) []domain.Turn {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.requests[i]
}

func (b *recordingBackend) requestCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.requests)
}

// scriptedBackend replays one canned response per request.
type scriptedBackend struct {
	mu        sync.Mutex
	responses [][]domain.ProviderEvent
}

func (b *scriptedBackend) FindModel(modelID string) (domain.Model, error) {
	return domain.Model{ID: modelID, Name: modelID, ProviderID: "scripted"}, nil
}

func (b *scriptedBackend) ChatStream(context.Context, domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.responses) == 0 {
		return nil, fmt.Errorf("no scripted response left")
	}
	events := b.responses[0]
	b.responses = b.responses[1:]
	ch := make(chan domain.ProviderEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

// adapterBackend sends requests to a real provider adapter.
type adapterBackend struct {
	adapter aiprovider.Adapter
	creds   aiprovider.Credentials
}

func (b adapterBackend) FindModel(modelID string) (domain.Model, error) {
	model, ok := domain.ModelByID(b.adapter.Models(), modelID)
	if !ok {
		return domain.Model{}, domain.ErrModelNotFound
	}
	return model, nil
}

func (b adapterBackend) ChatStream(ctx context.Context, req domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
	return b.adapter.Stream(ctx, b.creds, aiprovider.StreamRequest{
		Model:    req.Model,
		Turns:    req.Turns,
		Thinking: req.Thinking,
		Tools:    req.Tools,
	})
}

// orchestratedChat is the app chat service with the provider catalog and
// workspace instructions left out.
type orchestratedChat struct {
	agent orchestrator.AgentChatOrchestrator
}

func (c orchestratedChat) StreamSimple(context.Context, app.SimpleChatRequest) (<-chan domain.ClientEvent, error) {
	return nil, fmt.Errorf("not used")
}

func (c orchestratedChat) StreamAgent(ctx context.Context, req app.AgentChatRequest) (<-chan domain.ClientEvent, error) {
	return c.agent.Stream(ctx, orchestrator.AgentChatRequest{
		UserID:         req.UserID,
		WorkspaceID:    req.WorkspaceID,
		CurrentRunID:   req.CurrentRunID,
		ConversationID: req.ConversationID,
		Model:          req.Model,
		Turns:          req.Turns,
		Thinking:       req.Thinking,
		AgentPrompt:    req.AgentPrompt,
	})
}

// repoToolExecutor serves read_file and list_files from this repository.
type repoToolExecutor struct{}

func (repoToolExecutor) ExecuteTool(_ context.Context, req aitools.ExecutionRequest) domain.ToolResultPart {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(req.Arguments, &args)
	path := filepath.Join(root, filepath.Clean("/"+args.Path))

	result := domain.ToolResultPart{Name: req.ToolName}
	switch req.ToolName {
	case "read_file":
		data, err := os.ReadFile(path)
		if err != nil {
			result.Content, result.IsError = "Error: "+err.Error(), true
			break
		}
		if len(data) > 4000 {
			data = append(data[:4000], "\n... (truncated)"...)
		}
		result.Content = string(data)
	case "list_files":
		entries, err := os.ReadDir(path)
		if err != nil {
			result.Content, result.IsError = "Error: "+err.Error(), true
			break
		}
		var names []string
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() {
				name += "/"
			}
			names = append(names, name)
		}
		result.Content = strings.Join(names, "\n")
	case "update_plan":
		result.Content = "Plan updated."
	default:
		result.Content, result.IsError = "Error: "+req.ToolName+" is not available in this test; use read_file or list_files.", true
	}
	return result
}

type conversationFlow struct {
	runs           app.AgentRunService
	conversations  app.ConversationService
	backend        *recordingBackend
	userID         int64
	workspaceID    int64
	conversationID int64
	model          string
}

func newConversationFlow(t *testing.T, backend orchestrator.ChatService, model string) *conversationFlow {
	t.Helper()
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	conn := db.Conn()
	user, err := conn.Exec(`INSERT INTO users (username, email, password) VALUES ('flow', 'flow@example.com', 'x')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := user.LastInsertId()
	workspace, err := conn.Exec(`INSERT INTO workspaces (user_id, name, status, image) VALUES (?, 'flow', 'stopped', 'ubuntu:22.04')`, userID)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	workspaceID, _ := workspace.LastInsertId()

	conversations := app.NewConversationService(storage.NewConversationRepository(conn))
	conv, err := conversations.CreateConversation(context.Background(), userID, workspaceID, model)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	recorder := &recordingBackend{ChatService: backend}
	agent := orchestrator.NewAgentChatOrchestrator(recorder, aitools.NewCatalog(), repoToolExecutor{}, approval.NewMemoryBroker(), question.NewMemoryBroker())
	runs := app.NewAgentRunService(context.Background(), storage.NewAgentRunRepository(conn), orchestratedChat{agent: agent}, conversations)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = runs.Shutdown(ctx)
	})

	return &conversationFlow{
		runs:           runs,
		conversations:  conversations,
		backend:        recorder,
		userID:         userID,
		workspaceID:    workspaceID,
		conversationID: conv.ID,
		model:          model,
	}
}

// send runs one user message to completion and fails the test if the run fails.
func (f *conversationFlow) send(t *testing.T, message string) {
	t.Helper()
	run, err := f.runs.StartRun(context.Background(), app.StartAgentRunRequest{
		UserID:         f.userID,
		WorkspaceID:    f.workspaceID,
		ConversationID: f.conversationID,
		Model:          f.model,
		Message:        domain.NewTextTurn(domain.RoleUser, message),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		current, err := f.runs.GetRun(context.Background(), f.userID, run.ID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		switch current.Status {
		case domain.AgentRunCompleted:
			return
		case domain.AgentRunFailed, domain.AgentRunCancelled:
			t.Fatalf("run %d ended %s: %s", run.ID, current.Status, current.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %d did not finish", run.ID)
}

func (f *conversationFlow) history(t *testing.T) []domain.Turn {
	t.Helper()
	turns, err := f.conversations.GetTurns(context.Background(), f.conversationID, f.userID)
	if err != nil {
		t.Fatalf("get turns: %v", err)
	}
	return turns
}

func TestConversationFlow_SavedHistoryMatchesWhatTheModelIsSent(t *testing.T) {
	read := func(id, path string) domain.ToolCall {
		return domain.ToolCall{ID: id, Type: "function", Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"` + path + `"}`}}
	}
	backend := &scriptedBackend{responses: [][]domain.ProviderEvent{
		// Message 1, step 1: parallel calls with no text or thinking, the
		// response shape whose calls the browser used to lose.
		{{ToolCalls: []domain.ToolCall{read("call-a", "go.mod"), read("call-b", "main.go"), read("call-c", "Makefile")}}},
		// Message 1, step 2: a step with thinking state, text, and a call.
		{
			{ReasoningDelta: "Checking the server.", ReasoningState: json.RawMessage(`[{"type":"thinking","thinking":"Checking the server.","signature":"sig"}]`)},
			{TextDelta: "One more file."},
			{ToolCalls: []domain.ToolCall{read("call-d", "internal/server/server.go")}},
		},
		{{TextDelta: "It is a Go server with an embedded Vue frontend."}},
		// Message 2.
		{{TextDelta: "The adapters register in internal/ai/module.go."}},
	}}
	flow := newConversationFlow(t, backend, "scripted-model")

	flow.send(t, "How is this project built?")
	afterFirst := flow.history(t)
	flow.send(t, "Where are providers registered?")

	if got := flow.backend.requestCount(); got != 4 {
		t.Fatalf("expected 4 model requests, got %d", got)
	}
	// The run's last request is its in-memory history; the saved history must
	// match it, plus the final reply.
	lastRequest := flow.backend.request(2)
	if got, want := describeTurns(afterFirst[:len(afterFirst)-1]), describeTurns(lastRequest); got != want {
		t.Fatalf("saved history differs from what the model was sent\nsaved:\n%s\nsent:\n%s", got, want)
	}
	// The next message's request is built from the saved history.
	secondRequest := flow.backend.request(3)
	want := describeTurns(append(append([]domain.Turn{}, afterFirst...), domain.NewTextTurn(domain.RoleUser, "Where are providers registered?")))
	if got := describeTurns(secondRequest); got != want {
		t.Fatalf("second message was not sent the saved history\nsent:\n%s\nwant:\n%s", got, want)
	}
	if calls := afterFirst[1].ToolCalls(); len(calls) != 3 {
		t.Fatalf("expected all three parallel calls to be saved, got %+v", calls)
	}
	assertToolPairing(t, secondRequest)

	final := flow.history(t)
	if got := final[len(final)-1].Text(); got != "The adapters register in internal/ai/module.go." {
		t.Fatalf("expected the second reply to be saved, got %q", got)
	}
}

// TestLive_ConversationFlow runs the same two-message flow against the real
// Anthropic API. It costs a few cents and is skipped unless DEVPAD_LIVE_TESTS=1
// and DEVPAD_TEST_ANTHROPIC_KEY are set.
func TestLive_ConversationFlow(t *testing.T) {
	if os.Getenv("DEVPAD_LIVE_TESTS") != "1" {
		t.Skip("set DEVPAD_LIVE_TESTS=1 to run live API tests")
	}
	key := os.Getenv("DEVPAD_TEST_ANTHROPIC_KEY")
	if key == "" {
		t.Skip("set DEVPAD_TEST_ANTHROPIC_KEY to run live Anthropic tests")
	}
	model := os.Getenv("DEVPAD_LIVE_MODEL")
	if model == "" {
		model = "claude-sonnet-5-5"
	}

	backend := adapterBackend{adapter: anthropic.NewAdapter(), creds: aiprovider.Credentials{APIKey: key}}
	flow := newConversationFlow(t, backend, model)

	flow.send(t, "How is this project built? Read the Makefile, go.mod, and main.go in parallel, then explain.")
	flow.send(t, "Now explain how the AI provider adapters under internal/ai/provider are wired together.")

	history := flow.history(t)
	assertToolPairing(t, history)
	for i := 0; i < flow.backend.requestCount(); i++ {
		t.Logf("request %d: %d turns", i+1, len(flow.backend.request(i)))
	}
	t.Logf("saved history:\n%s", describeTurns(history))
}

func withoutSystemTurns(turns []domain.Turn) []domain.Turn {
	var kept []domain.Turn
	for _, turn := range turns {
		if turn.Role != domain.RoleSystem {
			kept = append(kept, turn)
		}
	}
	return kept
}

// describeTurns renders turns with everything a provider would be sent.
func describeTurns(turns []domain.Turn) string {
	var b strings.Builder
	for i, turn := range turns {
		fmt.Fprintf(&b, "%d %s:", i, turn.Role)
		for _, part := range turn.Parts {
			switch part.Kind {
			case domain.PartText:
				fmt.Fprintf(&b, " text=%q", part.Text)
			case domain.PartThinking:
				fmt.Fprintf(&b, " thinking=%q state=%s", part.Thinking.Text, part.Thinking.State)
			case domain.PartToolCall:
				fmt.Fprintf(&b, " call=%s:%s%s", part.ToolCall.ID, part.ToolCall.Function.Name, part.ToolCall.Function.Arguments)
			case domain.PartToolResult:
				content := part.ToolResult.Content
				if len(content) > 40 {
					content = content[:40] + "..."
				}
				fmt.Fprintf(&b, " result=%s:%s error=%v %q", part.ToolResult.ToolCallID, part.ToolResult.Name, part.ToolResult.IsError, content)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// assertToolPairing checks the rule providers enforce: each tool result
// answers a call in the assistant turn just before it, and every call is
// answered.
func assertToolPairing(t *testing.T, turns []domain.Turn) {
	t.Helper()
	for i := 0; i < len(turns); i++ {
		if turns[i].Role != domain.RoleAssistant {
			if turns[i].ToolResult() != nil {
				t.Fatalf("turn %d: tool result without an assistant turn before it\n%s", i, describeTurns(turns))
			}
			continue
		}
		pending := make(map[string]bool)
		for _, call := range turns[i].ToolCalls() {
			pending[call.ID] = true
		}
		for i+1 < len(turns) && turns[i+1].ToolResult() != nil {
			i++
			id := turns[i].ToolResult().ToolCallID
			if !pending[id] {
				t.Fatalf("turn %d: result %s has no matching call\n%s", i, id, describeTurns(turns))
			}
			delete(pending, id)
		}
		if len(pending) > 0 {
			t.Fatalf("calls without results before turn %d: %v\n%s", i, pending, describeTurns(turns))
		}
	}
}
