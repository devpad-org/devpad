package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	defaultSearchResults = 100
	maxSearchResults     = 500
	maxSearchContext     = 10
	// maxSearchFileBytes is the largest file a search reads.
	maxSearchFileBytes = 1 << 20
	// binarySniffBytes is how much of a file is checked for a NUL byte,
	// which marks it binary, as git does.
	binarySniffBytes = 8000
	maxSearchLineLen = 200
)

// searchSkippedDirs are directories of dependencies and caches a search
// leaves out unless its path filter names them.
var searchSkippedDirs = []string{"node_modules", "vendor", "__pycache__"}

// searchResult is a matching line, with the lines around it when the
// search asks for context.
type searchResult struct {
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Content string   `json:"content"`
	Before  []string `json:"before,omitempty"`
	After   []string `json:"after,omitempty"`
}

type searchRequest struct {
	Pattern    string `json:"pattern"`
	PathFilter string `json:"path_filter"`
	MaxResults int    `json:"max_results"`
	// ContextLines asks for this many lines before and after each match.
	ContextLines int `json:"context_lines"`
	// FilesOnly asks for the paths of matching files instead of lines. With
	// no pattern, every file the path filter matches does.
	FilesOnly bool `json:"files_only"`
}

type searchResponse struct {
	Results []searchResult `json:"results"`
	Files   []string       `json:"files,omitempty"`
	Count   int            `json:"count"`
	// Truncated says the search stopped at its most results, and more
	// match.
	Truncated bool `json:"truncated,omitempty"`
}

func handleSearchFiles(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Pattern == "" && !req.FilesOnly {
		writeErr(w, http.StatusBadRequest, "pattern is required")
		return
	}
	resp, err := searchFiles(r.Context(), req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func searchFiles(ctx context.Context, req searchRequest) (*searchResponse, error) {
	if req.MaxResults <= 0 {
		req.MaxResults = defaultSearchResults
	}
	req.MaxResults = min(req.MaxResults, maxSearchResults)
	req.ContextLines = clampInt(req.ContextLines, 0, maxSearchContext)

	var re *regexp.Regexp
	if req.Pattern != "" {
		var err error
		if re, err = regexp.Compile(req.Pattern); err != nil {
			return nil, fmt.Errorf("invalid regex: %v", err)
		}
	}
	matchPath, err := newPathMatcher(req.PathFilter)
	if err != nil {
		return nil, err
	}

	resp := &searchResponse{Results: []searchResult{}}
	found := 0
	for _, rel := range searchPaths(ctx, req.PathFilter) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !matchPath(rel) {
			continue
		}
		var matches []searchResult
		if re != nil {
			contextLines := req.ContextLines
			if req.FilesOnly {
				contextLines = 0
			}
			if matches = searchFile(rel, re, contextLines); len(matches) == 0 {
				continue
			}
		} else if !isSearchableFile(rel) {
			continue
		}

		if req.FilesOnly {
			if found == req.MaxResults {
				resp.Truncated = true
				break
			}
			resp.Files = append(resp.Files, rel)
			found++
			continue
		}
		if room := req.MaxResults - found; len(matches) > room {
			resp.Results = append(resp.Results, matches[:room]...)
			resp.Truncated = true
			break
		}
		resp.Results = append(resp.Results, matches...)
		found += len(matches)
	}
	resp.Count = found
	return resp, nil
}

// searchPaths lists the files a search reads, relative to the workspace
// root. In a git checkout they are the files git tracks and the new ones it
// does not ignore, so build output is left out and tracked dotfiles such as
// .devpad/ are in; elsewhere they are a walk that skips hidden directories.
// Either way the dependency directories are left out unless the filter
// names them.
func searchPaths(ctx context.Context, filter string) []string {
	var skipped []string
	for _, dir := range searchSkippedDirs {
		if !strings.Contains(filter, dir) {
			skipped = append(skipped, dir)
		}
	}
	paths, err := gitSearchPaths(ctx)
	if err != nil {
		return walkSearchPaths(skipped)
	}
	return slices.DeleteFunc(paths, func(rel string) bool {
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			if slices.Contains(skipped, part) {
				return true
			}
		}
		return false
	})
}

func gitSearchPaths(ctx context.Context) ([]string, error) {
	out, err := gitCommand(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, err
	}
	paths := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	// Tracked files come before new ones, and a conflicted file is listed
	// once for each of its stages.
	slices.Sort(paths)
	return slices.DeleteFunc(slices.Compact(paths), func(p string) bool { return p == "" }), nil
}

func walkSearchPaths(skipped []string) []string {
	var paths []string
	filepath.WalkDir(workspaceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip files we can't access
		}
		if d.IsDir() {
			if name := d.Name(); path != workspaceRoot && (strings.HasPrefix(name, ".") || slices.Contains(skipped, name)) {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(workspaceRoot, path); err == nil {
			paths = append(paths, rel)
		}
		return nil
	})
	return paths
}

// readSearchable reads a regular file small enough to search, reporting
// false for anything else and for a binary file.
func readSearchable(rel string) ([]byte, bool) {
	path := filepath.Join(workspaceRoot, rel)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSearchFileBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(data[:min(len(data), binarySniffBytes)], 0) >= 0 {
		return nil, false
	}
	return data, true
}

// isSearchableFile reports whether a search would read the file, for a
// files_only search without a pattern.
func isSearchableFile(rel string) bool {
	info, err := os.Lstat(filepath.Join(workspaceRoot, rel))
	return err == nil && info.Mode().IsRegular()
}

// searchFile returns the lines of a file that match, each with the lines
// around it that contextLines asks for.
func searchFile(rel string, re *regexp.Regexp, contextLines int) []searchResult {
	data, ok := readSearchable(rel)
	if !ok {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var results []searchResult
	for i, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		result := searchResult{File: filepath.ToSlash(rel), Line: i + 1, Content: truncate(line, maxSearchLineLen)}
		if contextLines > 0 {
			result.Before = truncateAll(lines[max(i-contextLines, 0):i])
			result.After = truncateAll(lines[i+1 : min(i+1+contextLines, len(lines))])
		}
		results = append(results, result)
	}
	return results
}

func truncateAll(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = truncate(line, maxSearchLineLen)
	}
	return out
}

// truncate cuts s to at most maxLen bytes, on a character.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	cut := maxLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// newPathMatcher matches the paths a search's path filter picks. A filter
// with glob characters is a glob, where * and ? stay within a directory, **
// spans directories and {a,b} is either, matched against the end of a path
// from a directory boundary: *.ts matches every .ts file, and
// internal/**/*.go every .go file under any internal/. Any other filter is
// text a path contains, such as src/. An empty filter matches every path.
func newPathMatcher(filter string) (func(string) bool, error) {
	if filter == "" {
		return func(string) bool { return true }, nil
	}
	if !strings.ContainsAny(filter, "*?[{") {
		return func(rel string) bool { return strings.Contains(filepath.ToSlash(rel), filter) }, nil
	}
	re, err := globRegexp(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid path_filter: %v", err)
	}
	return func(rel string) bool { return re.MatchString(filepath.ToSlash(rel)) }, nil
}

func globRegexp(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString(`^(?:.*/)?`)
	for i := 0; i < len(glob); i++ {
		switch glob[i] {
		case '*':
			switch {
			case strings.HasPrefix(glob[i:], "**/"):
				b.WriteString(`(?:.*/)?`)
				i += 2
			case strings.HasPrefix(glob[i:], "**"):
				b.WriteString(`.*`)
				i++
			default:
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		case '[':
			end := strings.IndexByte(glob[i+1:], ']')
			if end < 0 {
				return nil, fmt.Errorf("%q has a [ without a ]", glob)
			}
			class := glob[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + class + "]")
			i += end + 1
		case '{':
			end := strings.IndexByte(glob[i+1:], '}')
			if end < 0 {
				return nil, fmt.Errorf("%q has a { without a }", glob)
			}
			alternatives := strings.Split(glob[i+1:i+1+end], ",")
			for j, alternative := range alternatives {
				alternatives[j] = regexp.QuoteMeta(alternative)
			}
			b.WriteString("(?:" + strings.Join(alternatives, "|") + ")")
			i += end + 1
		default:
			b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
		}
	}
	b.WriteString(`$`)
	return regexp.Compile(b.String())
}
