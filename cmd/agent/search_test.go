package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// searchTree writes files under a new workspace root, path to content.
func searchTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	withWorkspaceRoot(t, root)
	return root
}

func search(t *testing.T, req searchRequest) *searchResponse {
	t.Helper()
	resp, err := searchFiles(context.Background(), req)
	if err != nil {
		t.Fatalf("search %+v: %v", req, err)
	}
	return resp
}

func resultFiles(resp *searchResponse) []string {
	var files []string
	for _, r := range resp.Results {
		if !slices.Contains(files, r.File) {
			files = append(files, r.File)
		}
	}
	return files
}

func TestSearchInAGitCheckoutReadsWhatGitWouldTrack(t *testing.T) {
	root := searchTree(t, map[string]string{
		".gitignore":            "dist/\n*.log\n",
		"main.go":               "func needle() {}\n",
		".devpad/pipeline.yml":  "run: needle\n",
		".github/workflows/ci":  "needle\n",
		"dist/bundle.js":        "needle\n",
		"debug.log":             "needle\n",
		"new.go":                "needle\n",
		"vendor/lib/lib.go":     "needle\n",
		"web/node_modules/x.js": "needle\n",
	})
	runGit(t, root, "init", "-q")
	runGit(t, root, "add", ".gitignore", "main.go", ".devpad", ".github", "vendor", "web")
	runGit(t, root, "commit", "-q", "-m", "init")

	got := resultFiles(search(t, searchRequest{Pattern: "needle"}))
	want := []string{".devpad/pipeline.yml", ".github/workflows/ci", "main.go", "new.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("searched %q, want %q", got, want)
	}
	if got := resultFiles(search(t, searchRequest{Pattern: "needle", PathFilter: "vendor/"})); !slices.Equal(got, []string{"vendor/lib/lib.go"}) {
		t.Fatalf("a filter naming vendor searched %q", got)
	}
}

func TestSearchOutsideAGitCheckoutSkipsHiddenAndDependencyDirectories(t *testing.T) {
	searchTree(t, map[string]string{
		"main.go":            "needle\n",
		".cache/x":           "needle\n",
		"node_modules/a.js":  "needle\n",
		"src/.env":           "needle\n",
		"src/__pycache__/x":  "needle\n",
		"src/deep/nested.go": "needle\n",
	})
	got := resultFiles(search(t, searchRequest{Pattern: "needle"}))
	want := []string{"main.go", "src/.env", "src/deep/nested.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("searched %q, want %q", got, want)
	}
}

func TestSearchSkipsBinaryFilesAndSymlinks(t *testing.T) {
	root := searchTree(t, map[string]string{
		"text.txt":  "needle\n",
		"image.bin": "needle\x00\x01",
	})
	if err := os.Symlink(filepath.Join(root, "text.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if got := resultFiles(search(t, searchRequest{Pattern: "needle"})); !slices.Equal(got, []string{"text.txt"}) {
		t.Fatalf("searched %q", got)
	}
}

func TestSearchReturnsContextAroundEachMatch(t *testing.T) {
	searchTree(t, map[string]string{"a.go": "one\ntwo\nneedle\nfour\nfive\nsix\n"})
	resp := search(t, searchRequest{Pattern: "needle", ContextLines: 2})
	if len(resp.Results) != 1 {
		t.Fatalf("results %+v", resp.Results)
	}
	r := resp.Results[0]
	if r.Line != 3 || !slices.Equal(r.Before, []string{"one", "two"}) || !slices.Equal(r.After, []string{"four", "five"}) {
		t.Fatalf("result %+v", r)
	}
	if r := search(t, searchRequest{Pattern: "one", ContextLines: 2}).Results[0]; len(r.Before) != 0 || len(r.After) != 2 {
		t.Fatalf("context at the top of a file %+v", r)
	}
}

func TestSearchSaysWhenItStoppedWithMoreToFind(t *testing.T) {
	searchTree(t, map[string]string{"a.go": "x\nx\n", "b.go": "x\n"})
	if resp := search(t, searchRequest{Pattern: "x", MaxResults: 3}); resp.Truncated || resp.Count != 3 {
		t.Fatalf("all of the matches: %+v", resp)
	}
	if resp := search(t, searchRequest{Pattern: "x", MaxResults: 2}); !resp.Truncated || resp.Count != 2 || len(resp.Results) != 2 {
		t.Fatalf("more than the most: %+v", resp)
	}
	if resp := search(t, searchRequest{Pattern: "x", FilesOnly: true, MaxResults: 1}); !resp.Truncated || !slices.Equal(resp.Files, []string{"a.go"}) {
		t.Fatalf("more files than the most: %+v", resp)
	}
}

func TestSearchFilesOnlyFindsFilesByName(t *testing.T) {
	searchTree(t, map[string]string{
		"frontend/src/App.vue":           "<template/>\n",
		"frontend/src/components/A.vue":  "<template/>\n",
		"frontend/src/main.ts":           "import App\n",
		"internal/auth/service.go":       "package auth\n",
		"internal/auth/service_test.go":  "package auth\n",
		"cmd/agent/internal_note.go.txt": "package auth\n",
	})
	for _, tc := range []struct {
		filter string
		want   []string
	}{
		{"*.vue", []string{"frontend/src/App.vue", "frontend/src/components/A.vue"}},
		{"src/*.{vue,ts}", []string{"frontend/src/App.vue", "frontend/src/main.ts"}},
		{"frontend/**/*.vue", []string{"frontend/src/App.vue", "frontend/src/components/A.vue"}},
		{"internal/**/*_test.go", []string{"internal/auth/service_test.go"}},
		{"internal/", []string{"internal/auth/service.go", "internal/auth/service_test.go"}},
		{"[A-Z]*.vue", []string{"frontend/src/App.vue", "frontend/src/components/A.vue"}},
	} {
		resp := search(t, searchRequest{FilesOnly: true, PathFilter: tc.filter})
		if !slices.Equal(resp.Files, tc.want) {
			t.Errorf("%s: %q, want %q", tc.filter, resp.Files, tc.want)
		}
	}
	if resp := search(t, searchRequest{Pattern: "package", FilesOnly: true, PathFilter: "*.go"}); !slices.Equal(resp.Files, []string{"internal/auth/service.go", "internal/auth/service_test.go"}) || len(resp.Results) != 0 {
		t.Fatalf("files with matches: %+v", resp)
	}
}

func TestSearchRefusesABadFilter(t *testing.T) {
	searchTree(t, map[string]string{"a.go": "x\n"})
	if _, err := searchFiles(context.Background(), searchRequest{Pattern: "x", PathFilter: "*.{go"}); err == nil || !strings.Contains(err.Error(), "path_filter") {
		t.Fatalf("expected the filter refused, got %v", err)
	}
}

func TestTruncateCutsOnACharacter(t *testing.T) {
	if got := truncate("aé", 2); got != "a…" {
		t.Fatalf("got %q", got)
	}
}
