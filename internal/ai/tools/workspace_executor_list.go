package tools

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/ai/domain"
)

const (
	// Keep recursive listings below the raw agent API caps to protect model context.
	maxListFilesDepth   = 6
	maxListFilesEntries = 1000
	listNarrowHint      = "List a subdirectory, give a lower max_depth, or use search_files with files_only and a path_filter to find files by name."
)

func (e *WorkspaceExecutor) listFiles(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	dir, _ := params["path"].(string)
	opts := agent.ListFilesOptions{}
	if value, ok := params["recursive"].(bool); ok {
		opts.Recursive = value
	}
	if value, ok := params["max_depth"].(float64); ok {
		opts.MaxDepth = clamp(int(value), 1, maxListFilesDepth)
	}
	if value, ok := params["max_entries"].(float64); ok {
		opts.MaxEntries = clamp(int(value), 1, maxListFilesEntries)
	}

	list, err := e.ws.ListFiles(ctx, userID, workspaceID, dir, opts)
	if err != nil {
		return toolFailure("list_files", "Error: %v", err)
	}
	if list == nil {
		return toolFailure("list_files", "Error: empty file list response")
	}
	return toolSuccess("list_files", listText(list))
}

// listText writes a listing as ls -R does: each directory's path from the
// project root, then its entries indented below it, a subdirectory with a
// trailing / and a file with its size. It keeps to maxToolTextBytes, cut
// after a whole line, and says when it or the listing stopped short.
func listText(list *agent.FileList) string {
	if len(list.Entries) == 0 {
		return "[The directory is empty.]"
	}
	// Entries come as a walk finds them, a directory before what is in it;
	// each directory's are written together, in the order they first came.
	var dirs []string
	byDir := map[string][]agent.FileEntry{}
	for _, entry := range list.Entries {
		parent := path.Dir(trimWorkspacePath(entry.Path))
		if _, ok := byDir[parent]; !ok {
			dirs = append(dirs, parent)
		}
		byDir[parent] = append(byDir[parent], entry)
	}

	var b strings.Builder
	shown := 0
	for _, dir := range dirs {
		header := dir + "/"
		if dir == "." {
			header = "./"
		}
		lines := []string{displayPath(header)}
		for _, entry := range byDir[dir] {
			if entry.Type == "directory" {
				lines = append(lines, "  "+displayPath(entry.Name+"/"))
			} else {
				lines = append(lines, "  "+displayPath(entry.Name)+"  "+fileSize(entry.Size))
			}
		}
		for i, line := range lines {
			need := len(line) + 1
			if i == 0 && len(lines) > 1 {
				// A directory's path goes only with an entry under it.
				need += len(lines[1]) + 1
			}
			if b.Len()+need > maxToolTextBytes {
				fmt.Fprintf(&b, "[Output truncated to %d bytes, after %d of %d entries. %s]", maxToolTextBytes, shown, len(list.Entries), listNarrowHint)
				return b.String()
			}
			b.WriteString(line + "\n")
			if i > 0 {
				shown++
			}
		}
	}
	if list.Truncated {
		fmt.Fprintf(&b, "[Stopped at %s, and there are more. %s]", plural(len(list.Entries), "entry", "entries"), listNarrowHint)
		return b.String()
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// fileSize writes a size in bytes as a person would read it, such as
// "812 B", "4.2 KB" or "13 MB".
func fileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	value, suffix := float64(bytes)/unit, "KB"
	for _, next := range []string{"MB", "GB", "TB"} {
		if value < unit {
			break
		}
		value, suffix = value/unit, next
	}
	if value < 10 {
		return fmt.Sprintf("%.1f %s", value, suffix)
	}
	return fmt.Sprintf("%.0f %s", value, suffix)
}

// displayPath quotes a path from the workspace that holds a control
// character, such as a newline, which would otherwise break the line it is
// written on into lines of its own.
func displayPath(p string) string {
	if strings.ContainsFunc(p, unicode.IsControl) {
		return strconv.Quote(p)
	}
	return p
}

func trimWorkspacePath(path string) string {
	if path == "/workspace" {
		return ""
	}
	return strings.TrimPrefix(path, "/workspace/")
}
