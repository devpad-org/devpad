package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
)

func dirEntry(path string) agent.FileEntry {
	return agent.FileEntry{Name: path[strings.LastIndexByte(path, '/')+1:], Path: path, Type: "directory"}
}

func fileEntry(path string, size int64) agent.FileEntry {
	return agent.FileEntry{Name: path[strings.LastIndexByte(path, '/')+1:], Path: path, Type: "file", Size: size}
}

func TestListTextWritesEachDirectoryWithItsEntries(t *testing.T) {
	// A walk finds a directory before what is in it.
	list := &agent.FileList{Entries: []agent.FileEntry{
		dirEntry("/workspace/cmd"),
		dirEntry("/workspace/cmd/agent"),
		fileEntry("/workspace/cmd/agent/main.go", 4300),
		dirEntry("/workspace/docs"),
		fileEntry("/workspace/docs/01-intro.md", 812),
		fileEntry("/workspace/go.mod", 120),
	}}
	want := strings.Join([]string{
		"./",
		"  cmd/",
		"  docs/",
		"  go.mod  120 B",
		"cmd/",
		"  agent/",
		"cmd/agent/",
		"  main.go  4.2 KB",
		"docs/",
		"  01-intro.md  812 B",
	}, "\n")
	if got := listText(list); got != want {
		t.Fatalf("listed\n%s\nwant\n%s", got, want)
	}
}

func TestListTextListsASubdirectoryUnderItsPath(t *testing.T) {
	list := &agent.FileList{Entries: []agent.FileEntry{
		fileEntry("/workspace/src/App.vue", 2048),
		dirEntry("/workspace/src/components"),
	}}
	if got, want := listText(list), "src/\n  App.vue  2.0 KB\n  components/"; got != want {
		t.Fatalf("listed %q, want %q", got, want)
	}
}

func TestListTextSaysWhenADirectoryIsEmpty(t *testing.T) {
	if got := listText(&agent.FileList{}); got != "[The directory is empty.]" {
		t.Fatalf("listed %q", got)
	}
}

func TestListTextQuotesANameThatWouldBreakItsLine(t *testing.T) {
	list := &agent.FileList{Entries: []agent.FileEntry{fileEntry("/workspace/evil\n  main.go", 1)}}
	if got, want := listText(list), "./\n  \"evil\\n  main.go\"  1 B"; got != want {
		t.Fatalf("listed %q, want %q", got, want)
	}
}

func TestListTextKeepsToWhatOneResultReturns(t *testing.T) {
	var entries []agent.FileEntry
	for d := range 20 {
		dir := fmt.Sprintf("/workspace/pkg%02d", d)
		entries = append(entries, dirEntry(dir))
		for f := range 50 {
			entries = append(entries, fileEntry(fmt.Sprintf("%s/file_with_a_long_name_%03d.go", dir, f), 100))
		}
	}
	got := listText(&agent.FileList{Entries: entries})
	body, note, ok := strings.Cut(got, "[Output truncated")
	if !ok || len(body) > maxToolTextBytes {
		t.Fatalf("expected the listing cut to %d bytes, got %d: %q", maxToolTextBytes, len(body), got[max(0, len(got)-200):])
	}
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "  ") {
		t.Fatalf("cut after a directory's path with none of its entries: %q", last)
	}
	shown := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "  ") {
			shown++
		}
	}
	if !strings.Contains(note, fmt.Sprintf("after %d of %d entries", shown, len(entries))) {
		t.Fatalf("note %q, after %d entries", note, shown)
	}
}

func TestFileSize(t *testing.T) {
	for size, want := range map[int64]string{
		0:                "0 B",
		1023:             "1023 B",
		1024:             "1.0 KB",
		4300:             "4.2 KB",
		20 * 1024:        "20 KB",
		13 * 1024 * 1024: "13 MB",
		3 << 40:          "3.0 TB",
	} {
		if got := fileSize(size); got != want {
			t.Errorf("fileSize(%d) = %q, want %q", size, got, want)
		}
	}
}
