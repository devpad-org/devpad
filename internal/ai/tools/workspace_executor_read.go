package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/attachment"
	"github.com/devpad-org/devpad/internal/ai/domain"
)

// readFile returns a file: an image as an image, a range of lines with
// their numbers when the call asks for one, and otherwise its text, cut
// after its last whole line that fits with a note of where to read on.
func (e *WorkspaceExecutor) readFile(ctx context.Context, req ExecutionRequest, params map[string]any) domain.ToolResultPart {
	path, _ := params["path"].(string)
	if path == "" {
		return toolFailure(req.ToolName, "Error: path is required")
	}
	data, err := e.ws.ReadFile(ctx, req.UserID, req.WorkspaceID, path)
	if err != nil {
		return toolFailure(req.ToolName, "Error: %v", err)
	}
	if width, height, ok := attachment.Probe(data); ok && e.images != nil {
		if !req.Vision {
			return toolSuccess(req.ToolName, fmt.Sprintf("%s is a %d×%d image. This model can't view images, so it isn't shown.", path, width, height))
		}
		return e.readImage(ctx, req, path, data)
	}
	_, hasStart := params["start_line"]
	_, hasEnd := params["end_line"]
	if hasStart || hasEnd || req.ToolName == "read_file_lines" {
		return readLines(req.ToolName, string(data), params)
	}
	return toolSuccess(req.ToolName, readWhole(string(data)))
}

// readImage returns an image file as an image the model can look at.
func (e *WorkspaceExecutor) readImage(ctx context.Context, req ExecutionRequest, path string, data []byte) domain.ToolResultPart {
	image, err := e.images.StoreToolImage(ctx, req.UserID, req.ConversationID, req.CurrentRunID, data)
	if err != nil {
		return toolFailure(req.ToolName, "Error: %s is an image that can't be shown: %v", path, err)
	}
	result := toolSuccess(req.ToolName, fmt.Sprintf("Image %s (%d×%d) is attached.", path, image.Width, image.Height))
	result.Images = []domain.ImagePart{image}
	return result
}

// fileLines splits text into its lines, the newline that ends the last one
// starting none of its own.
func fileLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// readWhole returns a file's text as it is when it fits, and otherwise as
// many whole lines as fit, with a note of the lines shown.
func readWhole(text string) string {
	if len(text) <= maxToolTextBytes {
		return text
	}
	total := len(fileLines(text))
	shown := domain.CutText(text, maxToolTextBytes)
	if i := strings.LastIndexByte(shown, '\n'); i >= 0 {
		shown = shown[:i+1]
		last := strings.Count(shown, "\n")
		return shown + fmt.Sprintf("\n[Showing lines 1–%d of %d: the file is longer than one read returns. Read on with start_line %d, or use search_files to find what you need.]", last, total, last+1)
	}
	return shown + fmt.Sprintf("\n\n[Showing the first %d bytes of line 1, which is longer than one read returns. The file has %s and %d bytes.]", len(shown), plural(total, "line", "lines"), len(text))
}

// readLines returns the lines a call asks for, each after its number, up to
// maxReadFileLines and maxToolTextBytes, with a note of where to read on
// when it stops short of them.
func readLines(toolName, text string, params map[string]any) domain.ToolResultPart {
	lines := fileLines(text)
	if len(lines) == 0 {
		return toolSuccess(toolName, "[The file is empty.]")
	}
	start, end := 1, len(lines)
	if value, ok := params["start_line"].(float64); ok {
		start = max(int(value), 1)
	}
	if value, ok := params["end_line"].(float64); ok && int(value) >= 1 {
		end = min(int(value), len(lines))
	}
	switch {
	case start > len(lines):
		return toolFailure(toolName, "Error: start_line %d is beyond the end of the file, which has %s", start, plural(len(lines), "line", "lines"))
	case end < start:
		return toolFailure(toolName, "Error: end_line must be greater than or equal to start_line")
	}

	var b strings.Builder
	last := start - 1
	for n := start; n <= end && n-start < maxReadFileLines; n++ {
		line := fmt.Sprintf("%d: %s\n", n, lines[n-1])
		if b.Len()+len(line) > maxToolTextBytes {
			if n == start {
				// A line longer than a read returns shows what fits.
				b.WriteString(domain.CutText(line, maxToolTextBytes))
				return toolSuccess(toolName, b.String()+fmt.Sprintf("\n\n[Line %d is longer than one read returns: showing its first %d bytes. The file has %s.]", n, maxToolTextBytes, plural(len(lines), "line", "lines")))
			}
			break
		}
		b.WriteString(line)
		last = n
	}
	shown := strings.TrimSuffix(b.String(), "\n")
	if last < end {
		shown += fmt.Sprintf("\n\n[Showing lines %d–%d of %d. Read on with start_line %d.]", start, last, len(lines), last+1)
	}
	return toolSuccess(toolName, shown)
}
