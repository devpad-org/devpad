package tools

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/workspace"
)

func TestAgentSystemPromptEncouragesSubAgentFanoutWithoutRateLimitCaveat(t *testing.T) {
	prompt := AgentSystemPrompt

	for _, want := range []string{
		"Use spawn_sub_agent proactively",
		"up to 3 child agents concurrently",
		"call wait_for_sub_agents before synthesizing",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q", want)
		}
	}
	if strings.Contains(prompt, "rate-limited") {
		t.Fatalf("prompt should not discourage sub-agents with stale rate-limit guidance")
	}
}

// fakeScopes resolves workspace 7 as a review sandbox and every other
// workspace as a plain one, except 13, which cannot be read.
type fakeScopes struct{ resolved int }

var errLocked = errors.New("database is locked")

func (f *fakeScopes) Resolve(_ context.Context, userID, workspaceID int64) (Scope, error) {
	f.resolved++
	if workspaceID == 13 {
		return Scope{}, errLocked
	}
	s := Scope{UserID: userID, WorkspaceID: workspaceID, Kind: workspace.KindInteractive}
	if workspaceID == 7 {
		s.Kind, s.ProjectID, s.Review = workspace.KindReview, 1, &ReviewScope{ReviewID: 3, MergeRequestID: 2, TargetBranch: "main"}
	}
	return s, nil
}

// reviewToolProvider offers one tool, only to a review.
type reviewToolProvider struct{ calls []ExecutionRequest }

func (p *reviewToolProvider) Contribute(_ context.Context, scope Scope) Contribution {
	if scope.Review == nil {
		return Contribution{}
	}
	return Contribution{Tools: []domain.ToolDefinition{{Type: "function", Function: domain.ToolFunction{Name: "submit_review"}}}}
}

func (p *reviewToolProvider) ExecuteTool(_ context.Context, req ExecutionRequest) (domain.ToolResultPart, bool) {
	if req.ToolName != "submit_review" {
		return domain.ToolResultPart{}, false
	}
	p.calls = append(p.calls, req)
	if req.Scope.Review == nil {
		return toolFailure(req.ToolName, "no review"), true
	}
	return toolSuccess(req.ToolName, "recorded"), true
}

func names(defs []domain.ToolDefinition) []string {
	var out []string
	for _, d := range defs {
		out = append(out, d.Function.Name)
	}
	return out
}

func TestProvidersAddWorkspaceToolsAndHandleTheirCalls(t *testing.T) {
	provider := &reviewToolProvider{}
	scopes := &fakeScopes{}
	catalog := NewCatalog()
	catalog.AddProvider(provider)
	catalog.SetScopeResolver(scopes)
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.AddProvider(provider)
	executor.SetScopeResolver(scopes)
	ctx := context.Background()

	review, err := catalog.Scope(ctx, 1, 7)
	if err != nil || review.Review == nil {
		t.Fatalf("expected the review's scope, got %+v %v", review, err)
	}
	if got, _ := catalog.For(ctx, review); !slices.Contains(names(got), "submit_review") || len(got) != len(AgentTools())+1 {
		t.Fatalf("review workspace tools = %v", names(got))
	}
	other, _ := catalog.Scope(ctx, 1, 8)
	if got, _ := catalog.For(ctx, other); slices.Contains(names(got), "submit_review") {
		t.Fatalf("other workspaces must not get the review tool: %v", names(got))
	}
	if _, err := catalog.Scope(ctx, 1, 13); !errors.Is(err, errLocked) {
		t.Fatalf("expected the resolver's error, got %v", err)
	}

	// Each call is resolved afresh, not taken from the run's start.
	result := executor.ExecuteTool(ctx, ExecutionRequest{UserID: 1, WorkspaceID: 7, ToolName: "submit_review", Arguments: []byte(`{}`)})
	if result.IsError || result.Content != "recorded" || len(provider.calls) != 1 || provider.calls[0].Scope.Review == nil {
		t.Fatalf("provider tool not routed with its scope: %+v %+v", result, provider.calls)
	}
	if result := executor.ExecuteTool(ctx, ExecutionRequest{UserID: 1, WorkspaceID: 8, ToolName: "submit_review", Arguments: []byte(`{}`)}); !result.IsError {
		t.Fatalf("a call outside the review must get the scope it is in: %+v", result)
	}
	if result := executor.ExecuteTool(ctx, ExecutionRequest{ToolName: "no_such_tool", Arguments: []byte(`{}`)}); !result.IsError {
		t.Fatalf("unknown tools must still fail: %+v", result)
	}

	// A call whose scope cannot be resolved reaches no provider.
	calls := len(provider.calls)
	result = executor.ExecuteTool(ctx, ExecutionRequest{UserID: 1, WorkspaceID: 13, ToolName: "submit_review", Arguments: []byte(`{}`)})
	if !result.IsError || !strings.Contains(result.Content, "did not run") || len(provider.calls) != calls {
		t.Fatalf("expected the call refused before the provider: %+v", result)
	}

	// Built-in tools never wait on a resolve.
	before := scopes.resolved
	executor.ExecuteTool(ctx, ExecutionRequest{UserID: 1, WorkspaceID: 13, ToolName: "update_plan", Arguments: []byte(`{}`)})
	if _, _, err := executor.ApprovalFor(ctx, ExecutionRequest{UserID: 1, WorkspaceID: 13, ToolName: "read_file"}); err != nil {
		t.Fatalf("a built-in tool's approval must not resolve a scope: %v", err)
	}
	if scopes.resolved != before {
		t.Fatalf("built-in tools resolved %d scopes", scopes.resolved-before)
	}
}

func TestProvidersWithoutAScopeResolverFailClosed(t *testing.T) {
	ctx := context.Background()
	provider := &reviewToolProvider{}
	catalog := NewCatalog()
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})

	// With no providers there is nothing to resolve.
	if s, err := catalog.Scope(ctx, 1, 7); err != nil || s.WorkspaceID != 7 {
		t.Fatalf("expected a bare scope, got %+v %v", s, err)
	}

	catalog.AddProvider(provider)
	executor.AddProvider(provider)
	if _, err := catalog.Scope(ctx, 1, 7); !errors.Is(err, errNoScopes) {
		t.Fatalf("expected runs refused without a resolver, got %v", err)
	}
	if result := executor.ExecuteTool(ctx, ExecutionRequest{UserID: 1, WorkspaceID: 7, ToolName: "submit_review", Arguments: []byte(`{}`)}); !result.IsError || len(provider.calls) != 0 {
		t.Fatalf("expected the call refused, got %+v", result)
	}
	if _, _, err := executor.ApprovalFor(ctx, ExecutionRequest{UserID: 1, WorkspaceID: 7, ToolName: "start_task"}); !errors.Is(err, errNoScopes) {
		t.Fatalf("expected the approval check to fail, got %v", err)
	}
}

// projectToolProvider adds prompt context and gates one of its tools.
type projectToolProvider struct{ reviewToolProvider }

func (projectToolProvider) Contribute(_ context.Context, scope Scope) Contribution {
	if scope.WorkspaceID != 7 {
		return Contribution{}
	}
	return Contribution{Prompt: "Project context"}
}

func (projectToolProvider) ApprovalFor(_ context.Context, req ExecutionRequest) (Approval, bool, error) {
	if req.ToolName == "broken" {
		return Approval{}, false, errors.New("lookup failed")
	}
	if req.Scope.WorkspaceID != req.WorkspaceID {
		return Approval{}, false, errors.New("approval checked without the call's scope")
	}
	return Approval{Title: "Start a background agent", Summary: "start"}, req.ToolName == "start_task", nil
}

func TestProvidersAddPromptContextAndApprovals(t *testing.T) {
	ctx := context.Background()
	scopes := &fakeScopes{}
	catalog := NewCatalog()
	catalog.SetScopeResolver(scopes)
	executor := NewWorkspaceExecutor(nil)
	executor.SetScopeResolver(scopes)
	catalog.AddProvider(&reviewToolProvider{})
	executor.AddProvider(&reviewToolProvider{})
	p := &projectToolProvider{}
	catalog.AddProvider(p)
	executor.AddProvider(p)

	for ws, want := range map[int64]string{7: "Project context", 8: ""} {
		scope, err := catalog.Scope(ctx, 1, ws)
		if err != nil {
			t.Fatal(err)
		}
		if _, got := catalog.For(ctx, scope); got != want {
			t.Fatalf("workspace %d: expected %q, got %q", ws, want, got)
		}
	}
	if approval, needed, err := executor.ApprovalFor(ctx, ExecutionRequest{UserID: 1, WorkspaceID: 7, ToolName: "start_task"}); err != nil || !needed || approval.Title != "Start a background agent" {
		t.Fatalf("expected the gate's approval, got %+v %v %v", approval, needed, err)
	}
	if _, needed, err := executor.ApprovalFor(ctx, ExecutionRequest{UserID: 1, WorkspaceID: 7, ToolName: "list_tasks"}); err != nil || needed {
		t.Fatal("expected no approval for other tools")
	}
	// A gate that cannot tell, or a scope that cannot be resolved, passes its
	// error on, so the call does not run.
	if _, needed, err := executor.ApprovalFor(ctx, ExecutionRequest{UserID: 1, WorkspaceID: 7, ToolName: "broken"}); err == nil || needed {
		t.Fatalf("expected the gate's error, got needed=%v err=%v", needed, err)
	}
	if _, needed, err := executor.ApprovalFor(ctx, ExecutionRequest{UserID: 1, WorkspaceID: 13, ToolName: "start_task"}); !errors.Is(err, errLocked) || needed {
		t.Fatalf("expected the resolver's error, got needed=%v err=%v", needed, err)
	}
}

// promptProvider adds one prompt section at a rank.
type promptProvider struct {
	prompt string
	rank   int
}

func (p promptProvider) Contribute(context.Context, Scope) Contribution {
	return Contribution{Prompt: p.prompt, Rank: p.rank}
}

func (promptProvider) ExecuteTool(context.Context, ExecutionRequest) (domain.ToolResultPart, bool) {
	return domain.ToolResultPart{}, false
}

func TestPromptSectionsFollowTheirRankThenTheirProviders(t *testing.T) {
	catalog := NewCatalog()
	catalog.SetScopeResolver(&fakeScopes{})
	for _, p := range []promptProvider{{prompt: "Review context"}, {prompt: "  "}, {prompt: "Project context"}, {prompt: "AGENTS.md", rank: RankInstructions}} {
		catalog.AddProvider(p)
	}
	scope, _ := catalog.Scope(context.Background(), 1, 8)
	if _, got := catalog.For(context.Background(), scope); got != "AGENTS.md\n\nReview context\n\nProject context" {
		t.Fatalf("unexpected prompt order %q", got)
	}
}
