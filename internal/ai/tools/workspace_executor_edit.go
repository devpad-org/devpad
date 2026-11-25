package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

const (
	// maxEditLinesListed bounds the line numbers an edit_file result lists.
	maxEditLinesListed = 10
	// maxMissShownLines and maxMissShownBytes bound the lines of the file a
	// failed edit shows as what its old_text nearly matches.
	maxMissShownLines = 40
	maxMissShownBytes = 4 * 1024
)

// textEdit is one replacement edit_file makes.
type textEdit struct {
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	ReplaceAll bool   `json:"replace_all"`
}

// textEdits is edit_file's list of edits. It also takes the list written as
// a JSON string, as some models send an array.
type textEdits []textEdit

func (t *textEdits) UnmarshalJSON(data []byte) error {
	var text string
	if json.Unmarshal(data, &text) == nil {
		data = []byte(text)
	}
	return json.Unmarshal(data, (*[]textEdit)(t))
}

// editRange is where an edit's new text lies in the file, in bytes.
type editRange struct{ start, end int }

func (e *WorkspaceExecutor) editFile(ctx context.Context, req ExecutionRequest) domain.ToolResultPart {
	var args struct {
		Path       string    `json:"path"`
		OldText    string    `json:"old_text"`
		NewText    string    `json:"new_text"`
		ReplaceAll bool      `json:"replace_all"`
		Edits      textEdits `json:"edits"`
	}
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		return toolFailure("edit_file", "Error: invalid tool arguments: %v", err)
	}
	edits := []textEdit(args.Edits)
	switch {
	case args.Path == "":
		return toolFailure("edit_file", "Error: path is required")
	case len(edits) > 0 && args.OldText != "":
		return toolFailure("edit_file", "Error: pass old_text and new_text for one edit, or edits for several, not both")
	case len(edits) == 0 && args.OldText == "":
		return toolFailure("edit_file", "Error: path and old_text are required")
	case len(edits) == 0:
		edits = []textEdit{{OldText: args.OldText, NewText: args.NewText, ReplaceAll: args.ReplaceAll}}
	}

	data, err := e.ws.ReadFile(ctx, req.UserID, req.WorkspaceID, args.Path)
	if err != nil {
		return toolFailure("edit_file", "Error reading file: %v", err)
	}
	content, ranges, err := applyEdits(string(data), edits)
	if err != nil {
		return toolFailure("edit_file", "Error: %v", err)
	}
	if err := e.ws.WriteFile(ctx, req.UserID, req.WorkspaceID, args.Path, []byte(content)); err != nil {
		return toolFailure("edit_file", "Error writing file: %v", err)
	}
	if len(ranges) == 1 {
		return toolSuccess("edit_file", fmt.Sprintf("Edited %s: %s.", args.Path, editedLines(content, ranges)))
	}
	return toolSuccess("edit_file", fmt.Sprintf("Made %d replacements in %s: %s.", len(ranges), args.Path, editedLines(content, ranges)))
}

// applyEdits makes edits in order, each to the content the ones before it
// left, and returns the new content with where each replacement's new text
// now lies. When one cannot be made, none is.
func applyEdits(content string, edits []textEdit) (string, []editRange, error) {
	failed := func(i int, err error) (string, []editRange, error) {
		if len(edits) > 1 {
			return "", nil, fmt.Errorf("edit %d of %d: %w. No edits were made", i+1, len(edits), err)
		}
		return "", nil, err
	}
	// A file with Windows line endings keeps them, whichever the model
	// wrote.
	crlf := strings.Contains(content, "\r\n")
	var ranges []editRange
	for i, edit := range edits {
		oldText, newText := edit.OldText, edit.NewText
		if crlf && !strings.Contains(oldText, "\r\n") {
			oldText, newText = toCRLF(oldText), toCRLF(newText)
		}
		switch {
		case oldText == "":
			return failed(i, errors.New("old_text is required"))
		case oldText == newText:
			return failed(i, errors.New("old_text and new_text are the same, so there is nothing to change"))
		}
		offsets := indexesOf(content, oldText)
		switch {
		case len(offsets) == 0:
			return failed(i, missedEdit(content, oldText))
		case len(offsets) > 1 && !edit.ReplaceAll:
			return failed(i, fmt.Errorf("old_text appears %d times, at %s; include more of the lines around it so that it appears once, or set replace_all to replace every one", len(offsets), lineList(content, offsets)))
		}

		var b strings.Builder
		var added []editRange
		prev := 0
		for _, off := range offsets {
			b.WriteString(content[prev:off])
			start := b.Len()
			b.WriteString(newText)
			added = append(added, editRange{start, b.Len()})
			prev = off + len(oldText)
		}
		b.WriteString(content[prev:])
		for j, r := range ranges {
			ranges[j] = editRange{
				start: shiftOffset(r.start, offsets, len(oldText), len(newText), false),
				end:   shiftOffset(r.end, offsets, len(oldText), len(newText), true),
			}
		}
		ranges = append(ranges, added...)
		content = b.String()
	}
	return content, ranges, nil
}

func toCRLF(s string) string {
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// indexesOf returns where sub starts in s, each occurrence after the end of
// the one before.
func indexesOf(s, sub string) []int {
	var offsets []int
	for from := 0; ; {
		i := strings.Index(s[from:], sub)
		if i < 0 {
			return offsets
		}
		offsets = append(offsets, from+i)
		from += i + len(sub)
	}
}

// shiftOffset moves an offset in content to where it lies once the text of
// oldLen bytes at each of offsets is replaced by text of newLen bytes. An
// offset inside replaced text moves to the start of its replacement, or to
// its end for the end of a range.
func shiftOffset(x int, offsets []int, oldLen, newLen int, end bool) int {
	shift := 0
	for _, off := range offsets {
		switch {
		case x <= off:
			return x + shift
		case x >= off+oldLen:
			shift += newLen - oldLen
		case end:
			return off + shift + newLen
		default:
			return off + shift
		}
	}
	return x + shift
}

func lineAt(content string, offset int) int {
	return 1 + strings.Count(content[:offset], "\n")
}

// editedLines lists the lines the edits' new text lies on, joining those
// that touch.
func editedLines(content string, ranges []editRange) string {
	type span struct{ first, last int }
	spans := make([]span, 0, len(ranges))
	for _, r := range ranges {
		first, last := lineAt(content, r.start), lineAt(content, r.start)
		if r.end > r.start {
			last = lineAt(content, r.end-1)
		}
		spans = append(spans, span{first, last})
	}
	slices.SortFunc(spans, func(a, b span) int { return a.first - b.first })
	var merged []span
	for _, s := range spans {
		if n := len(merged); n > 0 && s.first <= merged[n-1].last+1 {
			merged[n-1].last = max(merged[n-1].last, s.last)
			continue
		}
		merged = append(merged, s)
	}
	var parts []string
	for i, s := range merged {
		if i == maxEditLinesListed {
			parts = append(parts, fmt.Sprintf("and %d more", len(merged)-i))
			break
		}
		if s.first == s.last {
			parts = append(parts, fmt.Sprintf("%d", s.first))
		} else {
			parts = append(parts, fmt.Sprintf("%d–%d", s.first, s.last))
		}
	}
	noun := "line"
	if len(merged) > 1 || merged[0].first != merged[0].last {
		noun = "lines"
	}
	return noun + " " + strings.Join(parts, ", ")
}

// lineList names the lines offsets lie on, such as "lines 3, 17 and 40".
func lineList(content string, offsets []int) string {
	var lines []string
	for i, off := range offsets {
		if i == maxEditLinesListed {
			lines = append(lines, fmt.Sprintf("%d more", len(offsets)-i))
			break
		}
		lines = append(lines, fmt.Sprintf("%d", lineAt(content, off)))
	}
	if len(lines) == 1 {
		return "line " + lines[0]
	}
	return "lines " + strings.Join(lines[:len(lines)-1], ", ") + " and " + lines[len(lines)-1]
}

// readLinePrefix is what read_file_lines puts before each line it shows.
var readLinePrefix = regexp.MustCompile(`^\d+: ?`)

// missedEdit explains why old_text is not in content, saying what in the
// file it nearly matches when something does.
func missedEdit(content, oldText string) error {
	if stripped, ok := withoutLinePrefixes(oldText); ok && strings.Contains(content, stripped) {
		return errors.New(`old_text was not found: its lines start with the line numbers read_file_lines shows, such as "12: ", which are not part of the file. Leave them out`)
	}
	lines := strings.Split(content, "\n")
	if first, last, ok := whitespaceMatch(lines, oldText); ok {
		shown := strings.Join(lines[first:min(last+1, first+maxMissShownLines)], "\n")
		if len(shown) > maxMissShownBytes {
			shown = domain.CutText(shown, maxMissShownBytes) + "…"
		}
		return fmt.Errorf("old_text was not found exactly, but %s match it except for whitespace, such as indentation, tabs or trailing spaces. They are, exactly:\n%s", lineSpan(first+1, last+1), shown)
	}
	if firstLine := firstNonBlankLine(oldText); firstLine != "" {
		var at []int
		for i, line := range lines {
			if normalizeSpace(line) == firstLine {
				at = append(at, i+1)
			}
		}
		if len(at) > 0 {
			numbers := make([]string, 0, min(len(at), maxEditLinesListed))
			for _, n := range at[:min(len(at), maxEditLinesListed)] {
				numbers = append(numbers, fmt.Sprintf("%d", n))
			}
			return fmt.Errorf("old_text was not found. Its first line is in the file, at line %s, but what follows it differs: read those lines again, as the file may have changed since you read it", strings.Join(numbers, ", "))
		}
	}
	return errors.New("old_text was not found in the file. Read it again, as it may have changed since you read it")
}

func lineSpan(first, last int) string {
	if first == last {
		return fmt.Sprintf("line %d", first)
	}
	return fmt.Sprintf("lines %d–%d", first, last)
}

// withoutLinePrefixes strips the line numbers read_file_lines shows from
// every line of text, reporting false when a line lacks one.
func withoutLinePrefixes(text string) (string, bool) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i, line := range lines {
		prefix := readLinePrefix.FindString(line)
		if prefix == "" {
			return "", false
		}
		lines[i] = line[len(prefix):]
	}
	stripped := strings.Join(lines, "\n")
	if strings.HasSuffix(text, "\n") {
		stripped += "\n"
	}
	return stripped, true
}

// whitespaceMatch finds the first lines of the file that match oldText's
// lines once runs of whitespace are made one space and the ends trimmed,
// where its first line may end a line of the file and its last start one.
// It returns their indexes.
func whitespaceMatch(lines []string, oldText string) (int, int, bool) {
	want := strings.Split(strings.TrimSuffix(oldText, "\n"), "\n")
	for i := range want {
		want[i] = normalizeSpace(want[i])
	}
	if len(want) > len(lines) {
		return 0, 0, false
	}
	normalized := make([]string, len(lines))
	for i, line := range lines {
		normalized[i] = normalizeSpace(line)
	}
	for start := 0; start+len(want) <= len(lines); start++ {
		if windowMatches(normalized[start:start+len(want)], want) {
			return start, start + len(want) - 1, true
		}
	}
	return 0, 0, false
}

// windowMatches reports whether normalized lines of the file match want.
func windowMatches(lines, want []string) bool {
	last := len(want) - 1
	for i, w := range want {
		got := lines[i]
		switch {
		case last == 0:
			if w == "" || !strings.Contains(got, w) {
				return false
			}
		case i == 0:
			if !strings.HasSuffix(got, w) {
				return false
			}
		case i == last:
			if !strings.HasPrefix(got, w) {
				return false
			}
		case got != w:
			return false
		}
	}
	return true
}

func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func firstNonBlankLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = normalizeSpace(line); line != "" {
			return line
		}
	}
	return ""
}
