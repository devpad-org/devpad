package task

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	appdb "github.com/devpad-org/devpad/internal/database"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/workspace"
)

func TestHandoffTranscriptKeepsOnlyTheConversationText(t *testing.T) {
	turns := []domain.Turn{
		domain.NewTextTurn(domain.RoleSystem, "system prompt"),
		domain.NewTextTurn(domain.RoleUser, "Add a cart."),
		{Role: domain.RoleAssistant, Parts: []domain.Part{
			{Kind: domain.PartThinking, Text: "secret reasoning"},
			{Kind: domain.PartText, Text: "Reading the code first."},
			{Kind: domain.PartToolCall, ToolCall: &domain.ToolCall{ID: "1"}},
		}},
		domain.NewToolResultTurn("1", "read_file", "raw file contents", false),
		domain.NewTextTurn(domain.RoleAssistant, "The cart lives in cart.go."),
	}
	got := HandoffTranscript(turns, maxHandoffBytes)
	want := "**User:** Add a cart.\n\n**Assistant:** Reading the code first.\n\n**Assistant:** The cart lives in cart.go."
	if got != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", got, want)
	}
	if HandoffTranscript([]domain.Turn{domain.NewTextTurn(domain.RoleSystem, "x")}, 100) != "" {
		t.Fatal("expected an empty transcript for a conversation with no messages")
	}
}

func TestHandoffTranscriptKeepsTheGoalAndLatestMessagesWhenLong(t *testing.T) {
	turns := []domain.Turn{domain.NewTextTurn(domain.RoleUser, "GOAL: build the cart")}
	for i := 0; i < 50; i++ {
		turns = append(turns, domain.NewTextTurn(domain.RoleAssistant, strings.Repeat("step ", 40)))
	}
	turns = append(turns, domain.NewTextTurn(domain.RoleUser, "LATEST: now add tests"))
	got := HandoffTranscript(turns, 2000)
	if len(got) > 2000 {
		t.Fatalf("transcript is %d bytes, over the budget", len(got))
	}
	if !strings.HasPrefix(got, "**User:** GOAL: build the cart") || !strings.HasSuffix(got, "**User:** LATEST: now add tests") {
		t.Fatalf("expected the goal first and the latest message last:\n%s", got)
	}
	if !strings.Contains(got, "earlier messages left out]") {
		t.Fatalf("expected an omission marker:\n%s", got)
	}

	huge := []domain.Turn{domain.NewTextTurn(domain.RoleUser, strings.Repeat("é", 5000))}
	if got := HandoffTranscript(huge, 1000); len(got) > 1000 || !strings.HasSuffix(got, "[…]") {
		t.Fatalf("expected one oversized message clipped to the budget, got %d bytes", len(got))
	}
}

func TestTaskStartsFromAPublishedBaseBranch(t *testing.T) {
	e := setup(t)
	e.sandboxes.set(func(f *fakeSandboxes) { f.branches = []string{"feature/cart"} })
	ctx := context.Background()

	if _, err := e.svc.Create(ctx, owner, 1, CreateInput{Title: "x", BaseBranch: "feature/missing"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected an unpublished base to be refused, got %v", err)
	}
	onDefault, err := e.svc.Create(ctx, owner, 1, CreateInput{Title: "x", BaseBranch: "main"})
	if err != nil || onDefault.BaseBranch != "" {
		t.Fatalf("expected the default branch stored as no base, got %+v %v", onDefault, err)
	}

	task, err := e.svc.Create(ctx, owner, 1, CreateInput{
		Title: "Finish the cart", BaseBranch: "feature/cart", Start: true,
		AgentSettings: AgentSettings{Model: "claude-opus-5-5"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.BaseBranch != "feature/cart" || task.Status != StatusQueued {
		t.Fatalf("unexpected task: %+v", task)
	}
	e.waitTask(t, task.ID, hasRun)
	created := e.sandboxes.snapshot().created
	if len(created) != 1 || created[0].Branch != task.Branch || created[0].Base != "feature/cart" {
		t.Fatalf("expected the sandbox seeded from the base branch, got %+v", created)
	}
	prompt := e.runs.startedRequests()[0].Turns[0].Text()
	if !strings.Contains(prompt, "started from `feature/cart`") || !strings.Contains(prompt, "`devpad/main`") {
		t.Fatalf("prompt should name the base and still merge from main:\n%s", prompt)
	}
}

func TestHandoffStartsATaskWithTheConversation(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	in := HandoffInput{
		ConversationID: 7, Title: "Finish the cart", Instructions: "Wire up checkout.",
		AgentSettings: AgentSettings{Model: "claude-opus-5-5"},
	}

	if _, err := e.svc.Handoff(ctx, owner, 1, in); !errors.Is(err, project.ErrNoWorkspace) {
		t.Fatalf("expected no project workspace to be refused, got %v", err)
	}

	e.sandboxes.set(func(f *fakeSandboxes) {
		f.projectWS = &workspace.Workspace{ID: 50, UserID: owner, ProjectID: 1, Kind: workspace.KindInteractive}
	})
	e.convs.mu.Lock()
	e.convs.convs[7] = domain.Conversation{ID: 7, UserID: owner, WorkspaceID: 50, Title: "Cart work"}
	e.convs.convs[8] = domain.Conversation{ID: 8, UserID: owner, WorkspaceID: 101, Title: "In a sandbox"}
	e.convs.convs[9] = domain.Conversation{ID: 9, UserID: owner, WorkspaceID: 50, Title: "Empty"}
	e.convs.turns[7] = []domain.Turn{
		domain.NewTextTurn(domain.RoleUser, "Let's build a cart."),
		domain.NewToolResultTurn("1", "read_file", "RAW TOOL OUTPUT", false),
		domain.NewTextTurn(domain.RoleAssistant, "The cart needs a checkout step."),
	}
	e.convs.turns[8] = e.convs.turns[7]
	e.convs.mu.Unlock()

	for name, id := range map[string]int64{"another workspace": 8, "a missing conversation": 99} {
		bad := in
		bad.ConversationID = id
		if _, err := e.svc.Handoff(ctx, owner, 1, bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: expected refusal, got %v", name, err)
		}
	}
	if _, err := e.svc.Handoff(ctx, stranger, 1, in); err == nil {
		t.Error("expected another user's handoff to be refused")
	}
	empty := in
	empty.ConversationID = 9
	if _, err := e.svc.Handoff(ctx, owner, 1, empty); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected an empty conversation to be refused, got %v", err)
	}

	task, err := e.svc.Handoff(ctx, owner, 1, in)
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	if task.Status != StatusQueued || !strings.Contains(task.Body, "Wire up checkout.") || !strings.Contains(task.Body, `"Cart work"`) {
		t.Fatalf("unexpected task: %+v", task)
	}
	e.waitTask(t, task.ID, hasRun)
	prompt := e.runs.startedRequests()[0].Turns[0].Text()
	for _, want := range []string{"## The conversation this task continues", "**User:** Let's build a cart.", "**Assistant:** The cart needs a checkout step.", "Wire up checkout."} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "RAW TOOL OUTPUT") {
		t.Error("tool results must not be handed off")
	}
}

func TestRepositoryStoresBaseBranchAndHandoff(t *testing.T) {
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	conn := db.Conn()
	res, err := conn.Exec(`INSERT INTO users (username, email, password) VALUES ('u', 'u@example.com', 'x')`)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := res.LastInsertId()
	res, err = conn.Exec(`INSERT INTO projects (user_id, name, status) VALUES (?, 'p', 'ready')`, userID)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := res.LastInsertId()

	repo := NewRepository(conn)
	task := &Task{ProjectID: projectID, UserID: userID, Title: "t", Status: StatusTodo, BaseBranch: "feature/x", Handoff: "**User:** hi"}
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByID(context.Background(), task.ID)
	if err != nil || got.BaseBranch != "feature/x" || got.Handoff != "**User:** hi" {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}
