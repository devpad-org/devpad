package tools

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
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

// reviewToolProvider offers one tool, only in workspace 7.
type reviewToolProvider struct{ calls []ExecutionRequest }

func (p *reviewToolProvider) Tools(_ context.Context, _, workspaceID int64) []domain.ToolDefinition {
	if workspaceID != 7 {
		return nil
	}
	return []domain.ToolDefinition{{Type: "function", Function: domain.ToolFunction{Name: "submit_review"}}}
}

func (p *reviewToolProvider) ExecuteTool(_ context.Context, req ExecutionRequest) (domain.ToolResultPart, bool) {
	if req.ToolName != "submit_review" {
		return domain.ToolResultPart{}, false
	}
	p.calls = append(p.calls, req)
	return toolSuccess(req.ToolName, "recorded"), true
}

func TestProvidersAddWorkspaceToolsAndHandleTheirCalls(t *testing.T) {
	provider := &reviewToolProvider{}
	catalog := NewCatalog()
	catalog.AddProvider(provider)
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.AddProvider(provider)
	ctx := context.Background()

	names := func(defs []domain.ToolDefinition) []string {
		var out []string
		for _, d := range defs {
			out = append(out, d.Function.Name)
		}
		return out
	}
	if got := names(catalog.DefinitionsFor(ctx, 1, 7)); !slices.Contains(got, "submit_review") || len(got) != len(AgentTools())+1 {
		t.Fatalf("review workspace tools = %v", got)
	}
	if got := names(catalog.DefinitionsFor(ctx, 1, 8)); slices.Contains(got, "submit_review") {
		t.Fatalf("other workspaces must not get the review tool: %v", got)
	}

	result := executor.ExecuteTool(ctx, ExecutionRequest{UserID: 1, WorkspaceID: 7, ToolName: "submit_review", Arguments: []byte(`{}`)})
	if result.IsError || result.Content != "recorded" || len(provider.calls) != 1 {
		t.Fatalf("provider tool not routed: %+v", result)
	}
	if result := executor.ExecuteTool(ctx, ExecutionRequest{ToolName: "no_such_tool", Arguments: []byte(`{}`)}); !result.IsError {
		t.Fatalf("unknown tools must still fail: %+v", result)
	}
}

// projectToolProvider adds prompt context and gates one of its tools.
type projectToolProvider struct{ reviewToolProvider }

func (projectToolProvider) Prompt(_ context.Context, _, workspaceID int64) string {
	if workspaceID != 7 {
		return ""
	}
	return "Project context"
}

func (projectToolProvider) ApprovalFor(_ context.Context, req ExecutionRequest) (Approval, bool) {
	return Approval{Title: "Start a background agent", Summary: "start"}, req.ToolName == "start_task"
}

func TestProvidersAddPromptContextAndApprovals(t *testing.T) {
	ctx := context.Background()
	catalog := NewCatalog()
	executor := NewWorkspaceExecutor(nil)
	catalog.AddProvider(&reviewToolProvider{})
	executor.AddProvider(&reviewToolProvider{})
	p := &projectToolProvider{}
	catalog.AddProvider(p)
	executor.AddProvider(p)

	if got := catalog.PromptFor(ctx, 1, 7); got != "Project context" {
		t.Fatalf("expected the provider's context, got %q", got)
	}
	if got := catalog.PromptFor(ctx, 1, 8); got != "" {
		t.Fatalf("expected no context elsewhere, got %q", got)
	}
	if approval, needed := executor.ApprovalFor(ctx, ExecutionRequest{ToolName: "start_task"}); !needed || approval.Title != "Start a background agent" {
		t.Fatalf("expected the gate's approval, got %+v %v", approval, needed)
	}
	if _, needed := executor.ApprovalFor(ctx, ExecutionRequest{ToolName: "list_tasks"}); needed {
		t.Fatal("expected no approval for other tools")
	}
}
