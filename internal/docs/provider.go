package docs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aitools "github.com/devpad-org/devpad/internal/ai/tools"
	"github.com/devpad-org/devpad/internal/auth"
)

const readDocsTool = "read_devpad_docs"

var readDocsDefinition = domain.ToolDefinition{
	Type: "function",
	Function: domain.ToolFunction{
		Name:        readDocsTool,
		Description: "Read a page of devpad's own documentation: how devpad, the environment you and the user are working in, works. Read the page that covers it before answering a question about devpad itself, such as why a push was refused or how merge requests, tasks, services or budgets work.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"page":{"type":"string","description":"The page's slug, as listed in your instructions."}},"required":["page"]}`),
	},
}

// Users finds out whether a user is an admin, who may read the admin pages.
type Users interface {
	GetByID(ctx context.Context, id int64) (*auth.User, error)
}

// Provider gives every agent devpad's documentation: the IDE assistant, to
// answer the user's questions about devpad, and task and review agents, to
// know how devpad treats their work (why a push was refused, how a pipeline
// gates their merge request).
type Provider struct {
	users Users
}

var _ aitools.ToolProvider = (*Provider)(nil)

// NewProvider creates the documentation provider.
func NewProvider(users Users) *Provider { return &Provider{users: users} }

// offered reports whether a run gets the documentation: any run in a
// workspace, whether the assistant's, a task's or a review's.
func offered(scope aitools.Scope) bool {
	return scope.WorkspaceID != 0
}

// admin reports whether the run's user is an admin. Failing to find out
// leaves the admin pages out.
func (p *Provider) admin(ctx context.Context, userID int64) bool {
	u, err := p.users.GetByID(ctx, userID)
	return err == nil && u != nil && u.IsAdmin
}

// Contribute lists the pages in the system prompt and offers the tool.
func (p *Provider) Contribute(ctx context.Context, scope aitools.Scope) aitools.Contribution {
	if !offered(scope) {
		return aitools.Contribution{}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Devpad documentation\n\nYou are running inside devpad, a self-hosted IDE whose projects, branches, merge requests, tasks, agents, services, pipelines and budgets work in ways of their own. When you need to know how devpad works, or why it did something (refused a push, held a task, stopped an agent), read the page that covers it with %s and go by it rather than guessing. The user can read the same pages under Docs in devpad's header, at /docs/<page>.\n\n<devpad_docs>\n", readDocsTool)
	for _, page := range Pages(p.admin(ctx, scope.UserID)) {
		fmt.Fprintf(&b, "<page>\n<slug>%s</slug>\n<title>%s</title>\n<summary>%s</summary>\n</page>\n", page.Slug, escapeXML.Replace(page.Title), escapeXML.Replace(page.Summary))
	}
	b.WriteString("</devpad_docs>")
	return aitools.Contribution{Tools: []domain.ToolDefinition{readDocsDefinition}, Prompt: b.String()}
}

var escapeXML = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// ExecuteTool reads a page.
func (p *Provider) ExecuteTool(ctx context.Context, req aitools.ExecutionRequest) (domain.ToolResultPart, bool) {
	if req.ToolName != readDocsTool {
		return domain.ToolResultPart{}, false
	}
	if !offered(req.Scope) {
		return toolError("devpad's documentation is only offered to agents running in a workspace"), true
	}
	var args struct {
		Page string `json:"page"`
	}
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		return toolError("invalid arguments: %v", err), true
	}
	admin := p.admin(ctx, req.Scope.UserID)
	slug := strings.Trim(strings.TrimSpace(args.Page), "/")
	slug = strings.TrimPrefix(slug, "docs/")
	page, ok := Find(slug, admin)
	if !ok {
		var slugs []string
		for _, p := range Pages(admin) {
			slugs = append(slugs, p.Slug)
		}
		return toolError("there is no page %q. The pages are: %s", args.Page, strings.Join(slugs, ", ")), true
	}
	return domain.ToolResultPart{Name: readDocsTool, Content: "# " + page.Title + "\n\n" + page.PlainBody()}, true
}

// toolError reports a failed call. The "Error:" prefix is what the IDE uses
// to show a tool call as failed.
func toolError(format string, args ...any) domain.ToolResultPart {
	return domain.ToolResultPart{Name: readDocsTool, Content: "Error: " + fmt.Sprintf(format, args...), IsError: true}
}
