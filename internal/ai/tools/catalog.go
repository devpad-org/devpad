package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// Catalog exposes the current tool definitions and system prompt.
type Catalog struct {
	mu        sync.RWMutex
	providers []ToolProvider
	scopes    ScopeResolver
}

// errNoScopes reports providers added without a way to tell them what a
// run is. Runs fail rather than reach a provider that cannot tell.
var errNoScopes = errors.New("tool providers are set up without a run scope resolver")

// NewCatalog creates a new tool catalog.
func NewCatalog() *Catalog {
	return &Catalog{}
}

// AddProvider adds workspace-specific tools to the catalog.
func (c *Catalog) AddProvider(p ToolProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.providers = append(c.providers, p)
}

// SetScopeResolver sets what tells providers what a run's workspace is.
func (c *Catalog) SetScopeResolver(r ScopeResolver) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scopes = r
}

// Definitions returns the tool definitions available to the AI agent.
func (c *Catalog) Definitions() []domain.ToolDefinition {
	return AgentTools()
}

// Scope resolves the scope of a run in the workspace. Without providers
// there is nothing to resolve it for.
func (c *Catalog) Scope(ctx context.Context, userID, workspaceID int64) (Scope, error) {
	c.mu.RLock()
	providers, scopes := c.providers, c.scopes
	c.mu.RUnlock()
	return resolveScope(ctx, scopes, len(providers) > 0, userID, workspaceID)
}

// resolveScope resolves a scope for providers, failing when there are
// providers but no resolver.
func resolveScope(ctx context.Context, scopes ScopeResolver, hasProviders bool, userID, workspaceID int64) (Scope, error) {
	if !hasProviders {
		return Scope{UserID: userID, WorkspaceID: workspaceID}, nil
	}
	if scopes == nil {
		return Scope{}, errNoScopes
	}
	return scopes.Resolve(ctx, userID, workspaceID)
}

// For returns the built-in tools plus those the providers offer a run in
// scope, and the context they add to its system prompt.
func (c *Catalog) For(ctx context.Context, scope Scope) ([]domain.ToolDefinition, string) {
	c.mu.RLock()
	providers := c.providers
	c.mu.RUnlock()
	defs := AgentTools()
	var contributions []Contribution
	for _, p := range providers {
		contribution := p.Contribute(ctx, scope)
		defs = append(defs, contribution.Tools...)
		if contribution.Prompt = strings.TrimSpace(contribution.Prompt); contribution.Prompt != "" {
			contributions = append(contributions, contribution)
		}
	}
	slices.SortStableFunc(contributions, func(a, b Contribution) int { return cmp.Compare(a.Rank, b.Rank) })
	sections := make([]string, len(contributions))
	for i, c := range contributions {
		sections[i] = c.Prompt
	}
	return defs, strings.Join(sections, "\n\n")
}

// SystemPrompt returns the system prompt for the AI agent.
func (c *Catalog) SystemPrompt() string {
	return AgentSystemPrompt
}

// AgentTools returns the tool definitions available to the AI agent.
func AgentTools() []domain.ToolDefinition {
	return []domain.ToolDefinition{
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "read_file",
				Description: "Read a file in the project. A file too long to return whole is cut after a whole line, with a note of the lines shown and the start_line to read on from. Pass start_line, end_line or both to read only those lines, at most 400 at a time; each is then shown after its line number, as \"12: \", which is not part of the file. Reading a PNG, JPEG, GIF or WebP image returns the image itself, so you can look at screenshots and other pictures.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path from the project root, e.g. src/main.ts"},"start_line":{"type":"integer","description":"Optional first line to read (1-indexed). Defaults to 1."},"end_line":{"type":"integer","description":"Optional last line to read (inclusive). Defaults to the end of the file; at most 400 lines are shown at once."}},"required":["path"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "summarize_file",
				Description: "Generate a concise AI-oriented summary of a repository file without returning the full contents. Use this for the gist of a file too long to read whole, or one you only need an overview of, before reading the parts you need. The summary is not the file's exact text: read the code you will change.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path from the project root, e.g. src/main.ts"},"focus":{"type":"string","description":"Optional focus for the summary, such as public API, side effects, tests, or risks."}},"required":["path"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "write_file",
				Description: "Create or overwrite a file in the project. Parent directories are created automatically. Always write the complete file content.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path from the project root"},"content":{"type":"string","description":"The complete file content to write"}},"required":["path","content"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "list_files",
				Description: "List files and directories at a given path in the project: each directory's path, then its entries indented below it, a directory with a trailing / and a file with its size. Recursive listing is opt-in and bounded; dependency and build directories such as .git, node_modules, vendor and dist are listed but not expanded. To find files by name, use search_files with files_only and a path_filter instead.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative directory path from the project root. Use empty string or / for root."},"recursive":{"type":"boolean","description":"When true, list descendants recursively. Defaults to false."},"max_depth":{"type":"integer","description":"Maximum recursive depth from the requested path. Defaults to 3, at most 6."},"max_entries":{"type":"integer","description":"Maximum entries to return, at most 1000. A recursive listing defaults to 500."}},"required":["path"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "delete_file",
				Description: "Delete a file or directory (recursively) from the project.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path from the project root"}},"required":["path"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "run_command",
				Description: "Execute a finite shell command in the project's workspace container and wait for it to finish, for up to timeout_seconds: a command still running then is stopped. A call returns after 4 minutes at most; a command still running then runs on until it ends or its timeout is up, and the result names its command_id for read_command_output's wait_for_exit. The working directory is /workspace (project root) unless cwd says otherwise. Use for installing packages, running tests, building, etc., and give a slow build or test suite a timeout_seconds it fits in. Do not use for dev servers, watchers, or commands that take longer than 10 minutes; use start_command instead. Output too long to return whole shows its start and end, and says how to read the part left out with read_command_output. You have sudo access — use it when commands require elevated privileges (e.g. apt install, systemctl). Commands using sudo require user approval before execution.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"The shell command to run, e.g. npm install express"},"cwd":{"type":"string","description":"Optional relative working directory from /workspace. Defaults to /workspace."},"timeout_seconds":{"type":"integer","description":"Optional seconds to wait for the command to finish before stopping it. Defaults to 120; at most 600."}},"required":["command"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "start_command",
				Description: "Start a long-running shell command in the workspace container and return immediately with a command_id. Use for dev servers, file watchers, commands like npm start that keep running, and commands that may take longer than run_command's longest timeout, 10 minutes. Read logs with read_command_output, wait for it to finish with read_command_output's wait_for_exit, and stop it with stop_command when finished. Commands using sudo require user approval before execution.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"The shell command to start, e.g. npm start"},"cwd":{"type":"string","description":"Optional relative working directory from /workspace. Defaults to /workspace."}},"required":["command"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "read_command_output",
				Description: "Read buffered stdout/stderr from a command started by start_command, or the rest of a long run_command output. Pass the previous next_cursor to read only new output. Use wait_ms for short polling when waiting for logs. To wait for a command to finish, set wait_for_exit rather than polling: the call returns once the command exits, or wait_ms passes, with its status, exit code and output since cursor, whose start and end it shows when it is too long to return whole.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command_id":{"type":"string","description":"The command_id returned by start_command, or named in run_command's output"},"cursor":{"type":"integer","description":"Cursor to read from. Use 0 for the first read or the prior next_cursor for subsequent reads."},"max_bytes":{"type":"integer","description":"Maximum output bytes to return. Defaults to 12288; at most 16384."},"wait_ms":{"type":"integer","description":"Optional milliseconds to wait for new output when none is available. Defaults to 0 and is capped at 5000. With wait_for_exit, how long to wait for the command to exit instead: defaults to 240000 (4 minutes), which is also the most; wait again for a command still running."},"wait_for_exit":{"type":"boolean","description":"When true, wait until the command exits rather than only until new output arrives. Defaults to false."}},"required":["command_id"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "stop_command",
				Description: "Stop a command started by start_command. This terminates the command process group, allowing dev servers and watchers to be cleaned up.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command_id":{"type":"string","description":"The command_id returned by start_command"}},"required":["command_id"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "search_files",
				Description: "Search the project's files for a regex pattern. Use this to find function definitions, usages, imports, or any text across the codebase. Returns matching lines as path:line:content, and the context_lines around them as path-line-content with -- between groups, as grep -n does. In a git checkout it searches the files git tracks and new ones it does not ignore, so build output is left out and dotfiles such as .devpad/ and .github/ are in; node_modules, vendor and __pycache__ are left out unless path_filter names them. Binary files are skipped. With files_only it returns only the paths of matching files, and without a pattern it lists the files path_filter matches, which finds files by name.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"Regex pattern to search for (Go regex syntax); start it with (?i) to ignore case. Required unless files_only is set."},"path_filter":{"type":"string","description":"Optional: search only files whose path matches a glob, where * stays within a directory, ** spans directories and {a,b} is either, such as *.ts, internal/**/*.go or src/*.{ts,vue}; or whose path contains a text without glob characters, such as src/."},"context_lines":{"type":"integer","description":"Optional lines to show before and after each match. Defaults to 0; at most 5."},"files_only":{"type":"boolean","description":"When true, return the paths of matching files instead of their lines. Defaults to false."},"max_results":{"type":"integer","description":"Maximum number of matches, or of files with files_only, to return (default 100, max 100)"}}}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "edit_file",
				Description: "Apply targeted edits to an existing file by replacing specific text. The old_text must appear exactly once in the file unless replace_all is set; include enough surrounding lines for uniqueness. To make several changes to one file, pass them together as edits rather than calling this once for each: they apply in order, and if one fails none is made. Returns the lines the new text is on, so you need not read the file again to find it. Use this instead of write_file when modifying existing files — it's faster and safer.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path from the project root"},"old_text":{"type":"string","description":"The exact text to find and replace, without the line numbers read_file shows (must appear exactly once in the file unless replace_all is set)"},"new_text":{"type":"string","description":"The text to replace old_text with"},"replace_all":{"type":"boolean","description":"Replace every occurrence of old_text instead of exactly one, such as to rename something throughout the file. Defaults to false."},"edits":{"type":"array","description":"Several edits to make to the file at once, instead of old_text and new_text. Each applies to the file as the edits before it left it.","items":{"type":"object","properties":{"old_text":{"type":"string","description":"The exact text to find and replace"},"new_text":{"type":"string","description":"The text to replace it with"},"replace_all":{"type":"boolean","description":"Replace every occurrence instead of exactly one"}},"required":["old_text","new_text"]}}},"required":["path"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "update_plan",
				Description: "Create or update your step-by-step execution plan for complex, multi-step tasks. Call this BEFORE starting work to declare your plan, then call it again after completing each step to update the status. The frontend displays this as a live checklist so the user can track your progress. Do NOT use this for simple questions, single-file edits, or tasks that can be completed in one or two steps.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"steps":{"type":"array","description":"The full list of plan steps with current statuses","items":{"type":"object","properties":{"title":{"type":"string","description":"Short description of the step"},"status":{"type":"string","enum":["pending","in_progress","completed","failed"],"description":"Current status of the step"}},"required":["title","status"]}}},"required":["steps"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "ask_user",
				Description: "Ask the user one or more concise clarification questions and wait for their answers. Use only when ambiguity materially affects the next action. The UI always lets the user type a custom answer if none of the provided choices fit.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"Optional short title for the question card."},"questions":{"type":"array","minItems":1,"maxItems":5,"items":{"type":"object","properties":{"id":{"type":"string","description":"Stable short identifier for this question."},"prompt":{"type":"string","description":"The question to show the user."},"type":{"type":"string","enum":["single_choice","multiple_choice","text"],"description":"Expected answer shape. Defaults to single_choice."},"options":{"type":"array","description":"Suggested choices for single_choice or multiple_choice questions.","items":{"type":"object","properties":{"value":{"type":"string","description":"Machine-readable option value."},"label":{"type":"string","description":"Human-readable option label."}},"required":["value","label"]}},"allow_custom":{"type":"boolean","description":"Whether to explicitly invite a custom answer. Defaults to true; the UI still allows custom answers for safety."},"placeholder":{"type":"string","description":"Optional placeholder for the custom answer field."}},"required":["id","prompt"]}}},"required":["questions"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "spawn_sub_agent",
				Description: "Start a child AI agent run for an independent subtask. The child appears nested under this run in the Agent Runs sidebar. Use agent_id from list_available_agents to assign the best purpose-built agent. Use wait_for_result only when you need this single child's final answer before continuing; otherwise keep the returned runId and call wait_for_sub_agents later.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"Complete instructions for the child agent, including all necessary context, scope, and expected output."},"agent_id":{"type":"string","description":"Optional agent ID from list_available_agents. Defaults to the built-in coding agent."},"model":{"type":"string","description":"Optional model ID from the models list_available_agents returns, all from the current model's provider: another provider's model is refused. Defaults to the agent's model in that list."},"effort":{"type":"string","description":"Optional reasoning effort, one of the efforts list_available_agents gives for the chosen model. Defaults to the agent's effort in that list when the child runs on the agent's model, else the model's default effort."},"wait_for_result":{"type":"boolean","description":"When true, wait for the child run to finish and return its final text in the summary field. Defaults to false."},"timeout_seconds":{"type":"integer","description":"Maximum seconds to wait when wait_for_result is true. Defaults to 240, which is also the maximum."}},"required":["prompt"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "list_available_agents",
				Description: "List the AI agents and models available to sub-agents in this workspace: each agent's purpose and the model and effort it runs on by default, and the models a sub-agent can use, with their prices, supported efforts and whether they accept images. Use this before spawn_sub_agent to pick the best agent_id and a model and effort suited to the subtask.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "wait_for_sub_agents",
				Description: "Wait for one or more previously spawned child AI agent runs from this parent run. Use this after spawning multiple sub-agents in parallel; pass their runIds and the tool returns ordered results with status, summary, error, timedOut, and stillRunning fields. A child marked stillRunning has not failed: it keeps working after the wait ends, so call this tool again to collect its result rather than redoing its work.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"run_ids":{"type":"array","description":"Child agent run IDs returned by spawn_sub_agent. Results are returned in the same order; pass one ID as a one-item array when waiting for a single child.","items":{"type":"integer"},"minItems":1,"maxItems":20},"timeout_seconds":{"type":"integer","description":"Maximum seconds to wait for all requested child runs. Defaults to 240, which is also the maximum."}},"required":["run_ids"]}`),
			},
		},
	}
}

// AgentSystemPrompt is the base system prompt shared by every agent; the
// selected agent's instructions are appended to it.
const AgentSystemPrompt = `You are an expert AI coding assistant integrated into Devpad, a web-based IDE. You have access to the user's project files through tools.

When helping the user:
1. Read files before modifying them to understand context.
2. Read a file whole when you will change it or need its exact code. Use summarize_file for the gist of a file too long to read whole, or one you only need an overview of, then read the parts you need with read_file's start_line and end_line.
3. Use edit_file for targeted changes instead of write_file for existing files, and pass several changes to one file together as its edits.
4. Use search_files to find relevant code across the codebase, with context_lines to see the code around each match without reading the file, and with files_only and a path_filter such as **/*.vue to find files by name.
5. Use list_files to understand project structure.
6. Use run_command for finite commands such as installing packages, running tests, or building. It stops a command still running after timeout_seconds, 2 minutes unless you give it more, at most 10 minutes, so give a slow build or test suite the time it needs rather than running it again. A call returns after 4 minutes at most: a command still running then runs on to its timeout, and read_command_output's wait_for_exit waits for it again. Use start_command for long-running dev servers or watchers such as npm start, and for commands that take longer than 10 minutes, then inspect logs with read_command_output, wait for a command to finish with read_command_output's wait_for_exit rather than polling, and stop the process with stop_command when it is no longer needed. You have sudo access for elevated privileges (e.g. sudo apt install, sudo systemctl). Use sudo when a command requires root permissions. The environment is a Debian 13 container.
7. When tool calls do not depend on each other's results, such as reading several files or running several searches, make them all in one response rather than one at a time: calls that only read run at the same time, and each response you wait for is another request.

Plan management:
- Only use update_plan for complex tasks that involve 3 or more distinct steps (e.g. implementing a feature across multiple files, multi-stage refactors, or tasks requiring research then implementation).
- Do NOT use a plan for simple questions, explanations, single-file edits, quick fixes, or tasks with only one or two steps. Just do the work directly.
- When you do use a plan, mark the current step as "in_progress" before starting it.
- Mark it "completed" (or "failed") immediately after, then move to the next step.
- Keep all steps in each update_plan call — always send the full list with current statuses.

Clarifying questions:
- Use ask_user when a missing answer would materially change what you build, run, delete, or configure.
- Prefer making reasonable defaults yourself for routine choices; do not ask questions just to avoid deciding.
- Keep question prompts short and provide a few useful choices when possible.
- The user can always enter a custom answer if none of your choices fit, so treat custom answers as authoritative.

Sub-agents:
- Use spawn_sub_agent proactively when a task has independent research, review, implementation, or verification subtasks that can make progress without sharing live context.
- You may run up to 3 child agents concurrently for a parent run. Use parallel fan-out for independent subtasks, then call wait_for_sub_agents before synthesizing their results.
- When coordinating specialized work, call list_available_agents first, choose the best purpose-built agent, and pass its id as spawn_sub_agent.agent_id.
- Match the model to the subtask: list_available_agents also lists the models a sub-agent can use and what each costs. A cheaper or faster model, or a lower effort, often suits searching, summarizing, or mechanical edits; keep a stronger model for subtle design, debugging, or review. Leave model and effort unset to use the agent's defaults.
- Make each sub-agent prompt self-contained: include the goal, relevant constraints, and the expected result.
- By default the tool returns a child run ID immediately. Set wait_for_result=true when you need the child's answer before continuing; the result will include a summary field with the child's final response.
- For parallel fan-out/fan-in work, call spawn_sub_agent multiple times with wait_for_result=false, keep the returned runIds, then call wait_for_sub_agents with those runIds when you need all child results.
- A wait can end before a child does. A child reported as stillRunning has not failed and keeps working, so wait for it again instead of redoing its work or starting another child for it.

Format your chat replies using Markdown for readability:
- Use **bold** for emphasis and key terms.
- Use code blocks with language tags for code snippets.
- Use bullet points and numbered lists for steps or multiple items.
- Use headings (##, ###) to organize longer responses.
- Use emoji (✅, 📁, ⚡, 🔧, etc.) to highlight status and key points.

Text that others read later is not a chat reply: commit messages, comments on tasks and merge requests, merge request descriptions, review summaries and the summary that ends a task. Keep it plain and to the point, without emoji, and with headings only when it is long.

Be concise and precise. Write production-quality code. Explain what you're doing briefly.`
