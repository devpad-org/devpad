package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListFilesRecursiveRespectsDepthAndSkipsLargeDirectories(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.go"))
	writeTestFile(t, filepath.Join(root, "src", "app.go"))
	writeTestFile(t, filepath.Join(root, "src", "nested", "deep.go"))
	writeTestFile(t, filepath.Join(root, "node_modules", "pkg", "index.js"))
	writeTestFile(t, filepath.Join(root, ".git", "config"))

	result, err := listFiles(root, listFilesOptions{
		Recursive:  true,
		MaxDepth:   2,
		MaxEntries: 100,
	})
	if err != nil {
		t.Fatalf("listing files: %v", err)
	}

	paths := entryPaths(result.Entries)
	assertContainsPath(t, paths, filepath.Join(root, "main.go"))
	assertContainsPath(t, paths, filepath.Join(root, "src"))
	assertContainsPath(t, paths, filepath.Join(root, "src", "app.go"))
	assertContainsPath(t, paths, filepath.Join(root, "src", "nested"))
	assertNotContainsPath(t, paths, filepath.Join(root, "src", "nested", "deep.go"))
	assertContainsPath(t, paths, filepath.Join(root, "node_modules"))
	assertNotContainsPath(t, paths, filepath.Join(root, "node_modules", "pkg"))
	assertContainsPath(t, paths, filepath.Join(root, ".git"))
	assertNotContainsPath(t, paths, filepath.Join(root, ".git", "config"))
	if result.Truncated {
		t.Fatalf("expected complete result, got truncated")
	}
}

func TestListFilesRecursiveTruncatesAtMaxEntries(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.txt"))
	writeTestFile(t, filepath.Join(root, "b.txt"))
	writeTestFile(t, filepath.Join(root, "c.txt"))

	result, err := listFiles(root, listFilesOptions{
		Recursive:  true,
		MaxDepth:   1,
		MaxEntries: 2,
	})
	if err != nil {
		t.Fatalf("listing files: %v", err)
	}

	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(result.Entries), result.Entries)
	}
	if !result.Truncated {
		t.Fatalf("expected truncated result")
	}
}

func TestListFilesRecursiveSkipsUnreadableSubdirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read directories regardless of permission bits")
	}

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "accessible.txt"))
	writeTestFile(t, filepath.Join(root, "blocked", "hidden.txt"))
	blockedDir := filepath.Join(root, "blocked")
	if err := os.Chmod(blockedDir, 0); err != nil {
		t.Fatalf("making directory unreadable: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(blockedDir, 0o755); err != nil {
			t.Logf("restoring blocked directory permissions: %v", err)
		}
	})

	result, err := listFiles(root, listFilesOptions{
		Recursive:  true,
		MaxDepth:   3,
		MaxEntries: 100,
	})
	if err != nil {
		t.Fatalf("listing files: %v", err)
	}

	paths := entryPaths(result.Entries)
	assertContainsPath(t, paths, filepath.Join(root, "accessible.txt"))
	assertContainsPath(t, paths, blockedDir)
	assertNotContainsPath(t, paths, filepath.Join(blockedDir, "hidden.txt"))
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating parent directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}
}

func entryPaths(entries []fileEntry) map[string]bool {
	paths := make(map[string]bool, len(entries))
	for _, entry := range entries {
		paths[entry.Path] = true
	}
	return paths
}

func assertContainsPath(t *testing.T, paths map[string]bool, path string) {
	t.Helper()
	if !paths[path] {
		t.Fatalf("expected path %q in entries, got %+v", path, paths)
	}
}

func assertNotContainsPath(t *testing.T, paths map[string]bool, path string) {
	t.Helper()
	if paths[path] {
		t.Fatalf("did not expect path %q in entries, got %+v", path, paths)
	}
}
