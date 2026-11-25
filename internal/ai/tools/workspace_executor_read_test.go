package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// readWith runs a read tool with args on a file holding content.
func readWith(t *testing.T, tool, content, args string) (string, bool) {
	t.Helper()
	ops := &stubWorkspaceOps{readFileFn: func(context.Context, int64, int64, string) ([]byte, error) {
		return []byte(content), nil
	}}
	result := NewWorkspaceExecutor(ops).ExecuteTool(context.Background(), toolReq(7, 9, tool, []byte(args)))
	if result.Name != tool {
		t.Fatalf("a %s call got a %s result", tool, result.Name)
	}
	return result.Content, result.IsError
}

// lines is n lines of width bytes each, numbered from 1.
func lines(n, width int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		line := fmt.Sprintf("line %d ", i)
		b.WriteString(line + strings.Repeat("x", max(width-len(line), 0)) + "\n")
	}
	return b.String()
}

func TestReadFileReturnsAFileThatFitsAsItIs(t *testing.T) {
	file := "package main\n\nfunc main() {}\n"
	if content, isError := readWith(t, "read_file", file, `{"path":"main.go"}`); isError || content != file {
		t.Fatalf("got %q", content)
	}
}

func TestReadFileCutsALongFileAfterAWholeLine(t *testing.T) {
	file := lines(1000, 99) // 100 bytes a line
	content, _ := readWith(t, "read_file", file, `{"path":"big.go"}`)
	shown := maxToolTextBytes / 100
	if !strings.HasPrefix(content, file[:shown*100]) || strings.Contains(content, fmt.Sprintf("line %d ", shown+1)) {
		t.Fatalf("expected the first %d whole lines, got %q", shown, content[len(content)-300:])
	}
	notice := fmt.Sprintf("\n\n[Showing lines 1–%d of 1000: the file is longer than one read returns. Read on with start_line %d, or use search_files to find what you need.]", shown, shown+1)
	if !strings.HasSuffix(content, notice) {
		t.Fatalf("expected notice %q, got %q", notice, content[len(content)-300:])
	}
}

func TestReadFileShowsTheStartOfALineLongerThanARead(t *testing.T) {
	content, _ := readWith(t, "read_file", strings.Repeat("a", maxToolTextBytes+1024), `{"path":"min.js"}`)
	if !strings.HasPrefix(content, strings.Repeat("a", maxToolTextBytes)+"\n\n[Showing the first 16384 bytes of line 1") || !strings.Contains(content, "The file has 1 line and 17408 bytes.") {
		t.Fatalf("got %q", content[maxToolTextBytes-10:])
	}
}

func TestReadFileReadsARangeOfNumberedLines(t *testing.T) {
	file := "one\ntwo\nthree\nfour\n"
	for args, want := range map[string]string{
		`{"path":"f","start_line":2,"end_line":3}`:  "2: two\n3: three",
		`{"path":"f","start_line":3}`:               "3: three\n4: four",
		`{"path":"f","end_line":1}`:                 "1: one",
		`{"path":"f","start_line":2,"end_line":99}`: "2: two\n3: three\n4: four",
	} {
		if content, isError := readWith(t, "read_file", file, args); isError || content != want {
			t.Errorf("%s: got %q, want %q", args, content, want)
		}
	}
}

func TestReadFileSaysWhereToReadOnAfterARange(t *testing.T) {
	content, _ := readWith(t, "read_file", lines(425, 10), `{"path":"f","start_line":1,"end_line":425}`)
	if !strings.Contains(content, "\n400: line 400") || strings.Contains(content, "401: line 401") || !strings.HasSuffix(content, "\n\n[Showing lines 1–400 of 425. Read on with start_line 401.]") {
		t.Fatalf("a range of more than %d lines, got %q", maxReadFileLines, content[len(content)-200:])
	}

	content, _ = readWith(t, "read_file", lines(100, 300), `{"path":"f","start_line":11}`)
	last := 10 + maxToolTextBytes/len(fmt.Sprintf("%d: %s", 50, lines(1, 300)))
	if !strings.HasSuffix(content, fmt.Sprintf("\n\n[Showing lines 11–%d of 100. Read on with start_line %d.]", last, last+1)) {
		t.Fatalf("a range of more than a read's bytes, got %q", content[len(content)-200:])
	}
	if len(content) > maxToolTextBytes+200 {
		t.Fatalf("returned %d bytes", len(content))
	}

	content, _ = readWith(t, "read_file", strings.Repeat("a", maxToolTextBytes*2)+"\nb\n", `{"path":"f","start_line":1}`)
	if !strings.HasPrefix(content, "1: aaa") || !strings.HasSuffix(content, "[Line 1 is longer than one read returns: showing its first 16384 bytes. The file has 2 lines.]") {
		t.Fatalf("a line longer than a read, got %q", content[len(content)-200:])
	}
}

func TestReadFileRefusesARangeOutsideTheFile(t *testing.T) {
	if content, isError := readWith(t, "read_file", "a\nb\nc\n", `{"path":"f","start_line":4}`); !isError || !strings.Contains(content, "start_line 4 is beyond the end of the file, which has 3 lines") {
		t.Fatalf("got %q", content)
	}
	if content, isError := readWith(t, "read_file", "a\nb\nc\n", `{"path":"f","start_line":3,"end_line":2}`); !isError || !strings.Contains(content, "end_line must be") {
		t.Fatalf("got %q", content)
	}
	if content, isError := readWith(t, "read_file", "", `{"path":"f","start_line":1}`); isError || content != "[The file is empty.]" {
		t.Fatalf("an empty file, got %q", content)
	}
}

func TestRetiredToolsStillAnswerButAreNotOffered(t *testing.T) {
	for _, d := range AgentTools() {
		for _, retired := range retiredTools {
			if d.Function.Name == retired {
				t.Fatalf("%s is still offered", retired)
			}
		}
	}
	if content, isError := readWith(t, "read_file_lines", "one\ntwo\n", `{"path":"f"}`); isError || content != "1: one\n2: two" {
		t.Fatalf("read_file_lines got %q", content)
	}
	if !builtinTools()["command_status"] {
		t.Fatal("command_status would go to the providers")
	}
}
