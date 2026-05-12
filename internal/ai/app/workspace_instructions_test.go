package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
)

type fakeWorkspaceInstructionFiles struct {
	list      *agent.FileList
	listErr   error
	readData  []byte
	readErr   error
	readPaths []string
}

func (f *fakeWorkspaceInstructionFiles) ListFiles(context.Context, int64, int64, string, agent.ListFilesOptions) (*agent.FileList, error) {
	return f.list, f.listErr
}

func (f *fakeWorkspaceInstructionFiles) ReadFile(_ context.Context, _ int64, _ int64, path string) ([]byte, error) {
	f.readPaths = append(f.readPaths, path)
	return f.readData, f.readErr
}

func TestAGENTSInstructionSourceReadsRootAGENTS(t *testing.T) {
	files := &fakeWorkspaceInstructionFiles{
		list: &agent.FileList{Entries: []agent.FileEntry{
			{Name: "README.md", Path: "README.md", Type: "file"},
			{Name: "AGENTS.md", Path: "AGENTS.md", Type: "file"},
		}},
		readData: []byte("  Use table-driven tests.  \n"),
	}
	source := NewAGENTSInstructionSource(files)

	instructions, err := source.Instructions(context.Background(), 7, 9)
	if err != nil {
		t.Fatalf("expected instructions, got error: %v", err)
	}
	if instructions != "Use table-driven tests." {
		t.Fatalf("unexpected instructions: %q", instructions)
	}
	if len(files.readPaths) != 1 || files.readPaths[0] != "AGENTS.md" {
		t.Fatalf("expected AGENTS.md read, got %+v", files.readPaths)
	}
}

func TestAGENTSInstructionSourceIgnoresMissingRootAGENTS(t *testing.T) {
	files := &fakeWorkspaceInstructionFiles{
		list: &agent.FileList{Entries: []agent.FileEntry{{Name: "README.md", Path: "README.md", Type: "file"}}},
	}
	source := NewAGENTSInstructionSource(files)

	instructions, err := source.Instructions(context.Background(), 7, 9)
	if err != nil {
		t.Fatalf("expected missing AGENTS.md to be ignored, got %v", err)
	}
	if instructions != "" {
		t.Fatalf("expected no instructions, got %q", instructions)
	}
	if len(files.readPaths) != 0 {
		t.Fatalf("expected no read without AGENTS.md, got %+v", files.readPaths)
	}
}

func TestAGENTSInstructionSourceSurfacesReadErrors(t *testing.T) {
	readErr := errors.New("agent unavailable")
	files := &fakeWorkspaceInstructionFiles{
		list:    &agent.FileList{Entries: []agent.FileEntry{{Name: "AGENTS.md", Path: "AGENTS.md", Type: "file"}}},
		readErr: readErr,
	}
	source := NewAGENTSInstructionSource(files)

	_, err := source.Instructions(context.Background(), 7, 9)
	if !errors.Is(err, readErr) {
		t.Fatalf("expected read error, got %v", err)
	}
}

func TestFormatAGENTSInstructionsTruncatesLargeFiles(t *testing.T) {
	content := formatAGENTSInstructions([]byte(strings.Repeat("x", maxAgentsInstructionBytes+1)))
	if !strings.Contains(content, "[AGENTS.md truncated to 16384 bytes.]") {
		t.Fatalf("expected truncation notice, got %q", content[len(content)-80:])
	}
}
