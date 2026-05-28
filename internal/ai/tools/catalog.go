package tools

import (
	"encoding/json"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// Catalog exposes the current tool definitions and system prompt.
type Catalog struct{}

// NewCatalog creates a new tool catalog.
func NewCatalog() *Catalog {
	return &Catalog{}
}

// Definitions returns the tool definitions available to the AI agent.
func (c *Catalog) Definitions() []domain.ToolDefinition {
	return AgentTools()
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
				Description: "Read the contents of a file in the project. Results are truncated for large files; use read_file_lines for targeted sections.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path from the project root, e.g. src/main.ts"}},"required":["path"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "summarize_file",
				Description: "Generate a concise AI-oriented summary of a repository file without returning the full contents. Use this to understand large files before targeted reads.",
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
				Description: "List files and directories at a given path in the project. Recursive listing is opt-in and bounded; common large directories are not expanded.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative directory path from the project root. Use empty string or / for root."},"recursive":{"type":"boolean","description":"When true, list descendants recursively. Defaults to false."},"max_depth":{"type":"integer","description":"Maximum recursive depth from the requested path. Defaults server-side and is capped."},"max_entries":{"type":"integer","description":"Maximum entries to return. Defaults server-side and is capped."}},"required":["path"]}`),
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
				Description: "Execute a finite shell command in the project's workspace container and wait for it to finish. The working directory is /workspace (project root). Has a 30-second timeout. Use for installing packages, running tests, building, etc. Do not use for long-running dev servers or watchers; use start_command instead. You have sudo access — use it when commands require elevated privileges (e.g. apt install, systemctl). Commands using sudo require user approval before execution.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"The shell command to run, e.g. npm install express"}},"required":["command"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "start_command",
				Description: "Start a long-running shell command in the workspace container and return immediately with a command_id. Use for dev servers, file watchers, or commands like npm start that keep running. Read logs with read_command_output and stop it with stop_command when finished. Commands using sudo require user approval before execution.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"The shell command to start, e.g. npm start"},"cwd":{"type":"string","description":"Optional relative working directory from /workspace. Defaults to /workspace."}},"required":["command"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "read_command_output",
				Description: "Read buffered stdout/stderr from a command started by start_command. Pass the previous next_cursor to read only new output. Use wait_ms for short polling when waiting for logs.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command_id":{"type":"string","description":"The command_id returned by start_command"},"cursor":{"type":"integer","description":"Cursor to read from. Use 0 for the first read or the prior next_cursor for subsequent reads."},"max_bytes":{"type":"integer","description":"Maximum output bytes to return. Defaults to 12288 and is capped server-side."},"wait_ms":{"type":"integer","description":"Optional milliseconds to wait for new output when none is available. Defaults to 0 and is capped server-side."}},"required":["command_id"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "command_status",
				Description: "Get status metadata for a command started by start_command, including running/exited/stopped status and exit code when available.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command_id":{"type":"string","description":"The command_id returned by start_command"}},"required":["command_id"]}`),
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
				Description: "Search for a regex pattern across all files in the project. Returns matching lines with file paths and line numbers. Use this to find function definitions, usages, imports, or any text across the codebase.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"Regex pattern to search for (Go regex syntax)"},"path_filter":{"type":"string","description":"Optional: filter by file name/path pattern, e.g. *.ts or src/"},"max_results":{"type":"integer","description":"Maximum number of results to return (default 100, max 100)"}},"required":["pattern"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "edit_file",
				Description: "Apply a targeted edit to an existing file by replacing a specific text occurrence. The old_text must appear exactly once in the file. Include enough surrounding lines for uniqueness. Use this instead of write_file when modifying existing files — it's faster and safer.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path from the project root"},"old_text":{"type":"string","description":"The exact text to find and replace (must appear exactly once in the file)"},"new_text":{"type":"string","description":"The text to replace old_text with"}},"required":["path","old_text","new_text"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "read_file_lines",
				Description: "Read a specific range of lines from a file. Lines are 1-indexed and capped per call. Use this instead of read_file for large files when you only need a specific section.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path from the project root"},"start_line":{"type":"integer","description":"First line to read (1-indexed)"},"end_line":{"type":"integer","description":"Last line to read (inclusive)"}},"required":["path","start_line","end_line"]}`),
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
				Name:        "spawn_sub_agent",
				Description: "Start a child AI agent run for an independent subtask. The child appears nested under this run in the Agent Runs sidebar. Use agent_id from list_available_agents to assign the best purpose-built agent. Use wait_for_result only when you need this single child's final answer before continuing; otherwise keep the returned runId and call wait_for_sub_agents later.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"Complete instructions for the child agent, including all necessary context, scope, and expected output."},"agent_id":{"type":"string","description":"Optional agent ID from list_available_agents. Defaults to the baked-in default agent."},"model":{"type":"string","description":"Optional model ID. Defaults to the current model."},"wait_for_result":{"type":"boolean","description":"When true, wait for the child run to finish and return its final text in the summary field. Defaults to false."},"timeout_seconds":{"type":"integer","description":"Maximum seconds to wait when wait_for_result is true. Defaults to 120 and is capped at 600."}},"required":["prompt"]}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "list_available_agents",
				Description: "List the AI agents available in this workspace for the current user. Use this before coordinating specialized sub-agents so you can pass the best agent_id to spawn_sub_agent.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "wait_for_sub_agents",
				Description: "Wait for one or more previously spawned child AI agent runs from this parent run. Use this after spawning multiple sub-agents in parallel; pass their runIds and the tool returns ordered results with status, summary, error, and timedOut fields.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"run_ids":{"type":"array","description":"Child agent run IDs returned by spawn_sub_agent. Results are returned in the same order; pass one ID as a one-item array when waiting for a single child.","items":{"type":"integer"},"minItems":1,"maxItems":20},"timeout_seconds":{"type":"integer","description":"Maximum seconds to wait for all requested child runs. Defaults to 120 and is capped at 600."}},"required":["run_ids"]}`),
			},
		},
	}
}

// AgentSystemPrompt is the current system prompt for the built-in coding agent.
const AgentSystemPrompt = `You are an expert AI coding assistant integrated into Devpad, a web-based IDE. You have access to the user's project files through tools.

When helping the user:
1. Read files before modifying them to understand context.
2. Use summarize_file to understand large or unfamiliar files before deciding which sections to inspect with read_file_lines.
3. Use edit_file for targeted changes instead of write_file for existing files.
4. Use search_files to find relevant code across the codebase.
5. Use list_files to understand project structure.
6. Use run_command for finite commands such as installing packages, running tests, or building. Use start_command for long-running dev servers or watchers such as npm start, then inspect logs with read_command_output and stop the process with stop_command when it is no longer needed. You have sudo access for elevated privileges (e.g. sudo apt install, sudo systemctl). Use sudo when a command requires root permissions. The environment is a Debian 13 container.

Plan management:
- Only use update_plan for complex tasks that involve 3 or more distinct steps (e.g. implementing a feature across multiple files, multi-stage refactors, or tasks requiring research then implementation).
- Do NOT use a plan for simple questions, explanations, single-file edits, quick fixes, or tasks with only one or two steps. Just do the work directly.
- When you do use a plan, mark the current step as "in_progress" before starting it.
- Mark it "completed" (or "failed") immediately after, then move to the next step.
- Keep all steps in each update_plan call — always send the full list with current statuses.

Sub-agents:
- Use spawn_sub_agent proactively when a task has independent research, review, implementation, or verification subtasks that can make progress without sharing live context.
- You may run up to 3 child agents concurrently for a parent run. Use parallel fan-out for independent subtasks, then call wait_for_sub_agents before synthesizing their results.
- When coordinating specialized work, call list_available_agents first, choose the best purpose-built agent, and pass its id as spawn_sub_agent.agent_id.
- Make each sub-agent prompt self-contained: include the goal, relevant constraints, and the expected result.
- By default the tool returns a child run ID immediately. Set wait_for_result=true when you need the child's answer before continuing; the result will include a summary field with the child's final response.
- For parallel fan-out/fan-in work, call spawn_sub_agent multiple times with wait_for_result=false, keep the returned runIds, then call wait_for_sub_agents with those runIds when you need all child results.

Format your responses using Markdown for readability:
- Use **bold** for emphasis and key terms.
- Use code blocks with language tags for code snippets.
- Use bullet points and numbered lists for steps or multiple items.
- Use headings (##, ###) to organize longer responses.
- Use emoji (✅, 📁, ⚡, 🔧, etc.) to highlight status and key points.

Be concise and precise. Write production-quality code. Explain what you're doing briefly.`
