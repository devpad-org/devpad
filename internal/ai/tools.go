package ai

import "encoding/json"

// agentTools returns the tool definitions available to the AI agent.
func agentTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "read_file",
				Description: "Read the contents of a file in the project. Use this to understand existing code before modifying it.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path from the project root, e.g. src/main.ts"}},"required":["path"]}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "write_file",
				Description: "Create or overwrite a file in the project. Parent directories are created automatically. Always write the complete file content.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path from the project root"},"content":{"type":"string","description":"The complete file content to write"}},"required":["path","content"]}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "list_files",
				Description: "List files and directories at a given path in the project. Returns names, sizes, and whether each entry is a directory.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative directory path from the project root. Use empty string or / for root."}},"required":["path"]}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "delete_file",
				Description: "Delete a file or directory (recursively) from the project.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path from the project root"}},"required":["path"]}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "run_command",
				Description: "Execute a shell command in the project's workspace container. The working directory is /workspace (project root). Has a 30-second timeout. Use for installing packages, running tests, building, etc. You have sudo access — use it when commands require elevated privileges (e.g. apt install, systemctl). Commands using sudo require user approval before execution.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"The shell command to run, e.g. npm install express"}},"required":["command"]}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "search_files",
				Description: "Search for a regex pattern across all files in the project. Returns matching lines with file paths and line numbers. Use this to find function definitions, usages, imports, or any text across the codebase.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"Regex pattern to search for (Go regex syntax)"},"path_filter":{"type":"string","description":"Optional: filter by file name/path pattern, e.g. *.ts or src/"},"max_results":{"type":"integer","description":"Maximum number of results to return (default 100, max 500)"}},"required":["pattern"]}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "edit_file",
				Description: "Apply a targeted edit to an existing file by replacing a specific text occurrence. The old_text must appear exactly once in the file. Include enough surrounding lines for uniqueness. Use this instead of write_file when modifying existing files — it's faster and safer.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path from the project root"},"old_text":{"type":"string","description":"The exact text to find and replace (must appear exactly once in the file)"},"new_text":{"type":"string","description":"The text to replace old_text with"}},"required":["path","old_text","new_text"]}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "read_file_lines",
				Description: "Read a specific range of lines from a file. Lines are 1-indexed. Use this instead of read_file for large files when you only need a specific section.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path from the project root"},"start_line":{"type":"integer","description":"First line to read (1-indexed)"},"end_line":{"type":"integer","description":"Last line to read (inclusive)"}},"required":["path","start_line","end_line"]}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "update_plan",
				Description: "Create or update your step-by-step execution plan. Call this BEFORE starting work to declare your plan, then call it again after completing each step to update the status. The frontend displays this as a live checklist so the user can track your progress.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"steps":{"type":"array","description":"The full list of plan steps with current statuses","items":{"type":"object","properties":{"title":{"type":"string","description":"Short description of the step"},"status":{"type":"string","enum":["pending","in_progress","completed","failed"],"description":"Current status of the step"}},"required":["title","status"]}}},"required":["steps"]}`),
			},
		},
	}
}

// agentSystemPrompt returns the system prompt for the AI agent.
const agentSystemPrompt = `You are an expert AI coding assistant integrated into Devpad, a web-based IDE. You have access to the user's project files through tools.

When helping the user:
1. For multi-step tasks, ALWAYS call update_plan first to declare your steps, then call it again after completing each step to update the status.
2. Read files before modifying them to understand context.
3. Use edit_file for targeted changes instead of write_file for existing files.
4. Use search_files to find relevant code across the codebase.
5. Use list_files to understand project structure.
6. Use run_command to install packages, run tests, or build. You have sudo access for elevated privileges (e.g. sudo apt install, sudo systemctl). Use sudo when a command requires root permissions. The environment is a Debian 13 container.

Plan management:
- Call update_plan at the start of any task with more than one step.
- Mark the current step as "in_progress" before starting it.
- Mark it "completed" (or "failed") immediately after, then move to the next step.
- Keep all steps in each update_plan call — always send the full list with current statuses.

Format your responses using Markdown for readability:
- Use **bold** for emphasis and key terms.
- Use code blocks with language tags for code snippets.
- Use bullet points and numbered lists for steps or multiple items.
- Use headings (##, ###) to organize longer responses.
- Use emoji (✅, 📁, ⚡, 🔧, etc.) to highlight status and key points.

Be concise and precise. Write production-quality code. Explain what you're doing briefly.`
