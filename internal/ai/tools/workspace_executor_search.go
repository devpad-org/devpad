package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/ai/domain"
)

const (
	maxSearchFileResults   = 100
	maxSearchContextLines  = 5
	searchTruncationNotice = "\n\n[Output truncated to %d bytes. Narrow pattern or path_filter, or ask for fewer context_lines.]"
)

func (e *WorkspaceExecutor) searchFiles(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	opts := agent.SearchOptions{MaxResults: maxSearchFileResults}
	opts.Pattern, _ = params["pattern"].(string)
	opts.PathFilter, _ = params["path_filter"].(string)
	opts.FilesOnly, _ = params["files_only"].(bool)
	if opts.Pattern == "" && !opts.FilesOnly {
		return toolFailure("search_files", "Error: pattern is required, unless files_only is set")
	}
	if value, ok := params["max_results"].(float64); ok {
		opts.MaxResults = clamp(int(value), 1, maxSearchFileResults)
	}
	if value, ok := params["context_lines"].(float64); ok && !opts.FilesOnly {
		opts.ContextLines = clamp(int(value), 0, maxSearchContextLines)
	}

	resp, err := e.ws.SearchFiles(ctx, userID, workspaceID, opts)
	if err == nil && resp == nil {
		err = errors.New("empty search response")
	}
	if err != nil {
		return toolFailure("search_files", "Error: %v", err)
	}
	return toolSuccess("search_files", truncateToolText(searchText(resp, opts), maxToolTextBytes, searchTruncationNotice))
}

// searchText writes what a search found as grep does: a matching line as
// path:line:content, a line of context around it as path-line-content, and
// -- between lines that are not next to each other; or one path a line for
// the files of a files_only search.
func searchText(resp *agent.SearchResponse, opts agent.SearchOptions) string {
	var b strings.Builder
	for _, file := range resp.Files {
		b.WriteString(file + "\n")
	}
	writeSearchMatches(&b, resp.Results, opts.ContextLines > 0)
	switch {
	case b.Len() == 0 && opts.Pattern == "":
		return "[No files match.]"
	case b.Len() == 0:
		return "[No matches.]"
	case resp.Truncated && len(resp.Files) > 0:
		fmt.Fprintf(&b, "[Stopped at %s, and more match. Narrow pattern or path_filter to see the rest.]\n", plural(len(resp.Files), "file", "files"))
	case resp.Truncated:
		fmt.Fprintf(&b, "[Stopped at %s, and there are more. Narrow pattern or path_filter to see the rest.]\n", plural(len(resp.Results), "match", "matches"))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// writeSearchMatches writes matches file by file, in the order they came,
// merging the context of matches near each other.
func writeSearchMatches(b *strings.Builder, results []agent.SearchResult, withContext bool) {
	type line struct {
		text  string
		match bool
	}
	var file string
	lines := map[int]line{}
	flush := func() {
		numbers := make([]int, 0, len(lines))
		for n := range lines {
			numbers = append(numbers, n)
		}
		slices.Sort(numbers)
		for i, n := range numbers {
			if withContext && (i > 0 && n != numbers[i-1]+1 || i == 0 && b.Len() > 0) {
				b.WriteString("--\n")
			}
			if l := lines[n]; l.match {
				fmt.Fprintf(b, "%s:%d:%s\n", file, n, l.text)
			} else {
				fmt.Fprintf(b, "%s-%d-%s\n", file, n, l.text)
			}
		}
		clear(lines)
	}
	add := func(n int, text string, match bool) {
		if n < 1 || lines[n].match {
			return
		}
		lines[n] = line{text, match}
	}
	for _, r := range results {
		if r.File != file {
			flush()
			file = r.File
		}
		for i, text := range r.Before {
			add(r.Line-len(r.Before)+i, text, false)
		}
		add(r.Line, r.Content, true)
		for i, text := range r.After {
			add(r.Line+1+i, text, false)
		}
	}
	flush()
}
