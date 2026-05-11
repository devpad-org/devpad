package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func (e *WorkspaceExecutor) summarizeFile(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	path, _ := params["path"].(string)
	path = strings.TrimSpace(path)
	if path == "" {
		return toolFailure("summarize_file", "Error: path is required")
	}
	if e.summarizer == nil {
		return toolFailure("summarize_file", "Error: summarize_file is not configured")
	}
	focus, _ := params["focus"].(string)
	focus = strings.TrimSpace(focus)

	data, err := e.ws.ReadFile(ctx, userID, workspaceID, path)
	if err != nil {
		return toolFailure("summarize_file", "Error reading file: %v", err)
	}

	summary, err := e.summarizer.SummarizeFile(ctx, FileSummaryRequest{
		UserID:      userID,
		WorkspaceID: workspaceID,
		Path:        path,
		Focus:       focus,
		Content:     truncateToolText(lineNumberedContent(string(data)), maxSummaryInputBytes, summaryInputNotice),
	})
	if err != nil {
		return toolFailure("summarize_file", "Error summarizing file: %v", err)
	}

	summary = strings.TrimSpace(summary)
	if summary == "" {
		return toolFailure("summarize_file", "Error summarizing file: empty summary")
	}

	return toolSuccess("summarize_file", truncateToolText(summary, maxSummaryOutputBytes, truncationNoticeFormat))
}

func lineNumberedContent(content string) string {
	lines := strings.Split(content, "\n")
	var builder strings.Builder
	for i, line := range lines {
		if i > 0 {
			builder.WriteByte('\n')
		}
		fmt.Fprintf(&builder, "%d: %s", i+1, line)
	}
	return builder.String()
}
