package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func readRepo(t *testing.T, canonical string, req repoReadRequest) (int, repoReadResult, string) {
	t.Helper()
	body, _ := json.Marshal(req)
	rec := repoRequest(t, canonical, handleRepoRead, http.MethodPost, "/api/repo/read", bytes.NewReader(body))
	var result repoReadResult
	json.Unmarshal(rec.Body.Bytes(), &result)
	return rec.Code, result, rec.Body.String()
}

func TestRepoReadReadsDirsAndFilesAtOneCommit(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")
	if err := os.MkdirAll(filepath.Join(sandbox, "skills", "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	first := commitFile(t, sandbox, "skills/deploy/SKILL.md", "v1")
	commitFile(t, sandbox, "skills/notes.txt", "notes")
	commitFile(t, sandbox, "with space.txt", "spaced")
	if err := os.WriteFile(filepath.Join(sandbox, "skills", "deploy", "SKILL.md"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, sandbox, "commit", "-am", "v2")
	publish(t, canonical, sandbox, "task/1")
	tip := runGit(t, canonical, "rev-parse", "task/1")

	code, got, body := readRepo(t, canonical, repoReadRequest{
		Rev:   "task/1",
		Dirs:  []string{"skills", "missing", "skills/notes.txt"},
		Files: []string{"skills/deploy/SKILL.md", "missing.md", "skills", "with space.txt", "no such missing"},
	})
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if got.Commit != tip {
		t.Fatalf("expected the branch's tip %s, got %s", tip, got.Commit)
	}
	wantEntries := []repoTreeEntry{{Name: "deploy", Type: "tree"}, {Name: "notes.txt", Type: "blob"}}
	if d := got.Dirs[0]; !d.Found || d.Path != "skills" || !slices.Equal(d.Entries, wantEntries) || d.Truncated {
		t.Fatalf("unexpected listing: %+v", d)
	}
	// A path with nothing there, or a file, is no directory.
	for _, d := range got.Dirs[1:] {
		if d.Found || len(d.Entries) != 0 {
			t.Errorf("expected no directory at %s: %+v", d.Path, d)
		}
	}
	files := got.Files
	if !files[0].Found || string(files[0].Content) != "v2" {
		t.Fatalf("expected the file at the tip: %+v", files[0])
	}
	if !files[3].Found || string(files[3].Content) != "spaced" {
		t.Fatalf("expected a file with a space in its name: %+v", files[3])
	}
	// A missing file, a directory, and a missing name ending in "missing"
	// are no files.
	for _, i := range []int{1, 2, 4} {
		if files[i].Found || files[i].Content != nil {
			t.Errorf("expected no file at %s: %+v", files[i].Path, files[i])
		}
	}

	code, got, body = readRepo(t, canonical, repoReadRequest{Rev: first, Dirs: []string{"skills"}, Files: []string{"skills/deploy/SKILL.md"}})
	if code != http.StatusOK || got.Commit != first || len(got.Dirs[0].Entries) != 1 || string(got.Files[0].Content) != "v1" {
		t.Fatalf("expected everything at the earlier commit: %d %s", code, body)
	}
}

func TestRepoReadStopsAtItsBudget(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")
	commitFile(t, sandbox, "a.md", strings.Repeat("a", 100))
	commitFile(t, sandbox, "b.md", strings.Repeat("b", 100))
	commitFile(t, sandbox, "c.md", "c")
	commitFile(t, sandbox, "huge.md", strings.Repeat("h", maxRepoFileBytes+1))
	publish(t, canonical, sandbox, "task/1")

	// The file that reaches the budget is read; the ones after it are not,
	// and a file too large to read never counts against it.
	code, got, body := readRepo(t, canonical, repoReadRequest{Rev: "task/1", Files: []string{"huge.md", "a.md", "b.md", "c.md"}, MaxBytes: 150})
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if f := got.Files[0]; !f.Found || !f.TooLarge || f.Content != nil {
		t.Fatalf("expected the huge file left unread: %+v", f)
	}
	if len(got.Files[1].Content) != 100 || len(got.Files[2].Content) != 100 {
		t.Fatalf("expected a and b read: %+v", got.Files[1:3])
	}
	if f := got.Files[3]; !f.Found || !f.Skipped || f.Content != nil {
		t.Fatalf("expected c skipped past the budget: %+v", f)
	}
}

func TestRepoReadRefusesBadRequests(t *testing.T) {
	canonical := initCanonical(t)
	many := make([]string, maxRepoReadFiles+1)
	for i := range many {
		many[i] = fmt.Sprintf("f%d", i)
	}
	tests := []struct {
		name string
		req  repoReadRequest
		want int
	}{
		{"nothing to read", repoReadRequest{Rev: "main"}, http.StatusBadRequest},
		{"too many files", repoReadRequest{Rev: "main", Files: many}, http.StatusBadRequest},
		{"too many dirs", repoReadRequest{Rev: "main", Dirs: many[:maxRepoReadDirs+1]}, http.StatusBadRequest},
		{"a path out of the repo", repoReadRequest{Rev: "main", Files: []string{"../etc/passwd"}}, http.StatusBadRequest},
		{"an absolute path", repoReadRequest{Rev: "main", Dirs: []string{"/etc"}}, http.StatusBadRequest},
		{"a path over two lines", repoReadRequest{Rev: "main", Files: []string{"a\nmain:secret"}}, http.StatusBadRequest},
		{"an option for a rev", repoReadRequest{Rev: "--all", Files: []string{"README.md"}}, http.StatusBadRequest},
		{"no such branch", repoReadRequest{Rev: "nope", Files: []string{"README.md"}}, http.StatusNotFound},
		{"no such commit", repoReadRequest{Rev: strings.Repeat("0", 40), Files: []string{"README.md"}}, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _, body := readRepo(t, canonical, tt.req); code != tt.want {
				t.Fatalf("expected %d, got %d %s", tt.want, code, body)
			}
		})
	}
}

func TestParseTreeObject(t *testing.T) {
	oid := bytes.Repeat([]byte{0xab}, 20)
	var raw []byte
	for _, e := range []struct{ mode, name string }{{"40000", "dir"}, {"100644", "file"}, {"100755", "script"}, {"120000", "link"}, {"160000", "sub"}} {
		raw = append(raw, e.mode+" "+e.name+"\x00"...)
		raw = append(raw, oid...)
	}
	want := []repoTreeEntry{{"dir", "tree"}, {"file", "blob"}, {"script", "blob"}, {"link", "blob"}, {"sub", "commit"}}
	if got := parseTreeObject(raw, len(oid)); !slices.Equal(got.Entries, want) || got.Truncated {
		t.Fatalf("unexpected entries %+v", got)
	}
	// A cut-off entry ends the listing rather than misreading what follows.
	if got := parseTreeObject(raw[:len(raw)-5], len(oid)); len(got.Entries) != 4 {
		t.Fatalf("expected the cut-off entry dropped, got %+v", got.Entries)
	}

	raw = nil
	for i := 0; i <= maxRepoTreeEntries; i++ {
		raw = append(raw, fmt.Sprintf("100644 f%d\x00", i)...)
		raw = append(raw, oid...)
	}
	if got := parseTreeObject(raw, len(oid)); len(got.Entries) != maxRepoTreeEntries || !got.Truncated {
		t.Fatalf("expected %d entries, truncated; got %d %v", maxRepoTreeEntries, len(got.Entries), got.Truncated)
	}
}
