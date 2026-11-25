package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
)

// searchWith runs search_files with args against a workspace whose search
// returns resp, and returns the options it asked for and what it returned.
func searchWith(t *testing.T, args string, resp *agent.SearchResponse) (agent.SearchOptions, string, bool) {
	t.Helper()
	var asked agent.SearchOptions
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{searchFilesFn: func(_ context.Context, _, _ int64, opts agent.SearchOptions) (*agent.SearchResponse, error) {
		asked = opts
		return resp, nil
	}})
	result := executor.ExecuteTool(context.Background(), toolReq(7, 9, "search_files", []byte(args)))
	return asked, result.Content, result.IsError
}

func TestSearchFilesCapsWhatItAsksFor(t *testing.T) {
	asked, _, _ := searchWith(t, `{"pattern":"main","path_filter":"*.go","max_results":500,"context_lines":50}`, &agent.SearchResponse{})
	want := agent.SearchOptions{Pattern: "main", PathFilter: "*.go", MaxResults: maxSearchFileResults, ContextLines: maxSearchContextLines}
	if asked != want {
		t.Fatalf("asked %+v, want %+v", asked, want)
	}
	if asked, _, _ := searchWith(t, `{"files_only":true,"path_filter":"**/*.vue","context_lines":3}`, &agent.SearchResponse{}); asked.ContextLines != 0 || !asked.FilesOnly || asked.Pattern != "" {
		t.Fatalf("a files_only search asked %+v", asked)
	}
}

func TestSearchFilesNeedsAPatternUnlessFilesOnly(t *testing.T) {
	if _, content, isError := searchWith(t, `{"path_filter":"*.go"}`, &agent.SearchResponse{}); !isError || !strings.Contains(content, "pattern is required") {
		t.Fatalf("got %q", content)
	}
}

func TestSearchFilesWritesMatchesAsGrepDoes(t *testing.T) {
	resp := &agent.SearchResponse{Results: []agent.SearchResult{
		{File: "a.go", Line: 3, Content: "needle()", Before: []string{"one", "two"}, After: []string{"needle again", "five"}},
		{File: "a.go", Line: 4, Content: "needle again", Before: []string{"two", "needle()"}, After: []string{"five", "six"}},
		{File: "a.go", Line: 20, Content: "needle", Before: []string{"x", "y"}},
		{File: "b.go", Line: 1, Content: "needle", After: []string{"z"}},
	}}
	_, content, _ := searchWith(t, `{"pattern":"needle","context_lines":2}`, resp)
	want := strings.Join([]string{
		"a.go-1-one", "a.go-2-two", "a.go:3:needle()", "a.go:4:needle again", "a.go-5-five", "a.go-6-six",
		"--", "a.go-18-x", "a.go-19-y", "a.go:20:needle",
		"--", "b.go:1:needle", "b.go-2-z",
	}, "\n")
	if content != want {
		t.Fatalf("got\n%s\nwant\n%s", content, want)
	}

	_, content, _ = searchWith(t, `{"pattern":"needle"}`, &agent.SearchResponse{Results: []agent.SearchResult{{File: "a.go", Line: 3, Content: "needle"}, {File: "a.go", Line: 9, Content: "needle"}}})
	if content != "a.go:3:needle\na.go:9:needle" {
		t.Fatalf("without context, got %q", content)
	}
}

func TestSearchFilesSaysWhenItStopped(t *testing.T) {
	_, content, _ := searchWith(t, `{"pattern":"x","max_results":1}`, &agent.SearchResponse{Results: []agent.SearchResult{{File: "a.go", Line: 1, Content: "x"}}, Truncated: true})
	if content != "a.go:1:x\n[Stopped at 1 match, and there are more. Narrow pattern or path_filter to see the rest.]" {
		t.Fatalf("got %q", content)
	}
	_, content, _ = searchWith(t, `{"files_only":true,"path_filter":"*.go"}`, &agent.SearchResponse{Files: []string{"a.go", "b.go"}, Truncated: true})
	if content != "a.go\nb.go\n[Stopped at 2 files, and more match. Narrow pattern or path_filter to see the rest.]" {
		t.Fatalf("files, got %q", content)
	}
}

func TestSearchFilesSaysWhenNothingMatches(t *testing.T) {
	if _, content, _ := searchWith(t, `{"pattern":"x"}`, &agent.SearchResponse{}); content != "[No matches.]" {
		t.Fatalf("got %q", content)
	}
	if _, content, _ := searchWith(t, `{"files_only":true,"path_filter":"*.go"}`, &agent.SearchResponse{}); content != "[No files match.]" {
		t.Fatalf("got %q", content)
	}
}

func TestSearchFilesCapsItsOutput(t *testing.T) {
	var results []agent.SearchResult
	for i := range 100 {
		results = append(results, agent.SearchResult{File: "a.go", Line: i*20 + 1, Content: strings.Repeat("x", 199), After: []string{strings.Repeat("y", 199)}})
	}
	_, content, _ := searchWith(t, `{"pattern":"x","context_lines":1}`, &agent.SearchResponse{Results: results})
	if len(content) > maxToolTextBytes+200 || !strings.Contains(content, "[Output truncated to") {
		t.Fatalf("returned %d bytes, ending %q", len(content), content[len(content)-120:])
	}
}
