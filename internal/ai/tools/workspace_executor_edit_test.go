package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// editWith runs edit_file with args on a file holding content, and returns
// what it wrote (nil when it wrote nothing) and its result.
func editWith(t *testing.T, content, args string) (*string, string, bool) {
	t.Helper()
	var written *string
	ops := &stubWorkspaceOps{
		readFileFn: func(_ context.Context, _, _ int64, path string) ([]byte, error) {
			if path != "f.go" {
				t.Fatalf("read %q", path)
			}
			return []byte(content), nil
		},
		writeFileFn: func(_ context.Context, _, _ int64, path string, data []byte) error {
			text := string(data)
			written = &text
			return nil
		},
	}
	result := NewWorkspaceExecutor(ops).ExecuteTool(context.Background(), toolReq(1, 2, "edit_file", []byte(args)))
	return written, result.Content, result.IsError
}

// numbered is n lines, "l01" first.
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "l%02d\n", i)
	}
	return b.String()
}

func TestEditFileReplacesTextAndSaysWhereItLanded(t *testing.T) {
	written, content, isError := editWith(t, "a\nb\nc\n", `{"path":"f.go","old_text":"b","new_text":"B1\nB2"}`)
	if isError || written == nil || *written != "a\nB1\nB2\nc\n" {
		t.Fatalf("wrote %v: %q", written, content)
	}
	if content != "Edited f.go: lines 2–3." {
		t.Fatalf("got %q", content)
	}
	if _, content, _ := editWith(t, "a\nb\nc\n", `{"path":"f.go","old_text":"b\n","new_text":""}`); content != "Edited f.go: line 2." {
		t.Fatalf("a deletion, got %q", content)
	}
}

func TestEditFileMakesSeveralEditsInOrder(t *testing.T) {
	want := strings.Replace(strings.Replace(numbered(10), "l02\n", "x\ny\nz\n", 1), "l08", "eight", 1)
	for _, args := range []string{
		`{"path":"f.go","edits":[{"old_text":"l02\n","new_text":"x\ny\nz\n"},{"old_text":"l08","new_text":"eight"}]}`,
		// An edit before an earlier one moves where that one landed.
		`{"path":"f.go","edits":[{"old_text":"l08","new_text":"eight"},{"old_text":"l02\n","new_text":"x\ny\nz\n"}]}`,
		// Some models send the list as a JSON string.
		`{"path":"f.go","edits":"[{\"old_text\":\"l08\",\"new_text\":\"eight\"},{\"old_text\":\"l02\\n\",\"new_text\":\"x\\ny\\nz\\n\"}]"}`,
	} {
		written, content, isError := editWith(t, numbered(10), args)
		if isError || written == nil || *written != want {
			t.Fatalf("%s: wrote %v: %q", args, written, content)
		}
		if content != "Made 2 replacements in f.go: lines 2–4, 10." {
			t.Fatalf("%s: got %q", args, content)
		}
	}
}

func TestEditFileMakesNoEditWhenOneFails(t *testing.T) {
	written, content, isError := editWith(t, "a\nb\n", `{"path":"f.go","edits":[{"old_text":"a","new_text":"A"},{"old_text":"zzz","new_text":"Z"}]}`)
	if !isError || written != nil {
		t.Fatalf("wrote %v", written)
	}
	if !strings.HasPrefix(content, "Error: edit 2 of 2: old_text was not found") || !strings.HasSuffix(content, "No edits were made") {
		t.Fatalf("got %q", content)
	}
}

func TestEditFileReplacesEveryOccurrenceWhenAsked(t *testing.T) {
	written, content, _ := editWith(t, "foo bar foo\nkeep\nfoo\n", `{"path":"f.go","old_text":"foo","new_text":"baz","replace_all":true}`)
	if written == nil || *written != "baz bar baz\nkeep\nbaz\n" || content != "Made 3 replacements in f.go: lines 1, 3." {
		t.Fatalf("wrote %v: %q", written, content)
	}
	written, content, isError := editWith(t, "foo\nx\nfoo\n", `{"path":"f.go","old_text":"foo","new_text":"baz"}`)
	if !isError || written != nil || !strings.Contains(content, "old_text appears 2 times, at lines 1 and 3") || !strings.Contains(content, "replace_all") {
		t.Fatalf("an ambiguous edit wrote %v: %q", written, content)
	}
}

func TestEditFileKeepsWindowsLineEndings(t *testing.T) {
	written, _, isError := editWith(t, "a\r\nb\r\nc\r\n", `{"path":"f.go","old_text":"a\nb","new_text":"A\nB"}`)
	if isError || written == nil || *written != "A\r\nB\r\nc\r\n" {
		t.Fatalf("wrote %q", *written)
	}
}

func TestEditFileExplainsAMiss(t *testing.T) {
	file := "func a() {\n\treturn 1\n}\n\nx := 1\ny := 2\n"
	for _, tc := range []struct{ name, args, want string }{
		{"line numbers", `{"path":"f.go","old_text":"5: x := 1\n6: y := 2","new_text":"z"}`, "line numbers read_file shows"},
		{"whitespace", `{"path":"f.go","old_text":"func a() {\n    return 1\n}","new_text":"z"}`, "lines 1–3 match it except for whitespace, such as indentation, tabs or trailing spaces. They are, exactly:\nfunc a() {\n\treturn 1\n}"},
		{"first line", `{"path":"f.go","old_text":"x := 1\ny := 3","new_text":"z"}`, "Its first line is in the file, at line 5, but what follows it differs"},
		{"nothing", `{"path":"f.go","old_text":"nowhere","new_text":"z"}`, "old_text was not found in the file. Read it again"},
	} {
		written, content, isError := editWith(t, file, tc.args)
		if !isError || written != nil || !strings.Contains(content, tc.want) {
			t.Errorf("%s: wrote %v, got %q, want %q", tc.name, written, content, tc.want)
		}
	}
}

func TestEditFileRefusesConfusedArguments(t *testing.T) {
	for args, want := range map[string]string{
		`{"path":"f.go","old_text":"a","new_text":"b","edits":[{"old_text":"a","new_text":"b"}]}`: "not both",
		`{"path":"f.go","old_text":"a","new_text":"a"}`:                                           "the same",
		`{"path":"f.go"}`:                            "old_text are required",
		`{"old_text":"a","new_text":"b"}`:            "path is required",
		`{"path":"f.go","edits":[{"new_text":"b"}]}`: "old_text is required",
	} {
		if written, content, isError := editWith(t, "a\n", args); !isError || written != nil || !strings.Contains(content, want) {
			t.Errorf("%s: wrote %v, got %q, want %q", args, written, content, want)
		}
	}
}

func TestShiftOffsetMovesOffsetsInsideReplacedText(t *testing.T) {
	// "aaXXbb": XX at 2 replaced by "Y".
	for _, tc := range []struct {
		x, want int
		end     bool
	}{{1, 1, false}, {2, 2, false}, {3, 2, false}, {3, 3, true}, {4, 3, false}, {6, 5, false}} {
		if got := shiftOffset(tc.x, []int{2}, 2, 1, tc.end); got != tc.want {
			t.Errorf("shiftOffset(%d, end=%t) = %d, want %d", tc.x, tc.end, got, tc.want)
		}
	}
}
