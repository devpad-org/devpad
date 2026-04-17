package main

import (
	"testing"
)

func TestParseGitStatusOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []gitStatusEntry
	}{
		{
			name:     "empty output",
			input:    "",
			expected: []gitStatusEntry{},
		},
		{
			name:  "untracked file",
			input: "?? newfile.txt\n",
			expected: []gitStatusEntry{
				{Path: "newfile.txt", StatusCode: "??", Status: "untracked", Staged: false},
			},
		},
		{
			name:  "staged new file",
			input: "A  main.go\n",
			expected: []gitStatusEntry{
				{Path: "main.go", StatusCode: "A ", Status: "added", Staged: true},
			},
		},
		{
			name:  "unstaged modification",
			input: " M readme.md\n",
			expected: []gitStatusEntry{
				{Path: "readme.md", StatusCode: " M", Status: "modified", Staged: false},
			},
		},
		{
			name:  "staged modification",
			input: "M  readme.md\n",
			expected: []gitStatusEntry{
				{Path: "readme.md", StatusCode: "M ", Status: "modified", Staged: true},
			},
		},
		{
			name:  "dual-status MM emits two entries",
			input: "MM readme.md\n",
			expected: []gitStatusEntry{
				{Path: "readme.md", StatusCode: "MM", Status: "modified", Staged: true},
				{Path: "readme.md", StatusCode: "MM", Status: "modified", Staged: false},
			},
		},
		{
			name:  "staged add then modified in worktree (AM)",
			input: "AM newfile.go\n",
			expected: []gitStatusEntry{
				{Path: "newfile.go", StatusCode: "AM", Status: "added", Staged: true},
				{Path: "newfile.go", StatusCode: "AM", Status: "modified", Staged: false},
			},
		},
		{
			name:  "staged deletion",
			input: "D  old.go\n",
			expected: []gitStatusEntry{
				{Path: "old.go", StatusCode: "D ", Status: "deleted", Staged: true},
			},
		},
		{
			name:  "unstaged deletion",
			input: " D old.go\n",
			expected: []gitStatusEntry{
				{Path: "old.go", StatusCode: " D", Status: "deleted", Staged: false},
			},
		},
		{
			name:  "renamed file",
			input: "R  old.go -> new.go\n",
			expected: []gitStatusEntry{
				{Path: "new.go", StatusCode: "R ", Status: "renamed", Staged: true},
			},
		},
		{
			name:  "ignored file",
			input: "!! vendor/lib.so\n",
			expected: []gitStatusEntry{
				{Path: "vendor/lib.so", StatusCode: "!!", Status: "ignored", Staged: false},
			},
		},
		{
			name: "multiple files mixed statuses",
			input: "M  staged.go\n" +
				" M unstaged.go\n" +
				"MM both.go\n" +
				"?? new.txt\n" +
				"A  added.go\n",
			expected: []gitStatusEntry{
				{Path: "staged.go", StatusCode: "M ", Status: "modified", Staged: true},
				{Path: "unstaged.go", StatusCode: " M", Status: "modified", Staged: false},
				{Path: "both.go", StatusCode: "MM", Status: "modified", Staged: true},
				{Path: "both.go", StatusCode: "MM", Status: "modified", Staged: false},
				{Path: "new.txt", StatusCode: "??", Status: "untracked", Staged: false},
				{Path: "added.go", StatusCode: "A ", Status: "added", Staged: true},
			},
		},
		{
			name:  "staged add then deleted in worktree (AD)",
			input: "AD newfile.go\n",
			expected: []gitStatusEntry{
				{Path: "newfile.go", StatusCode: "AD", Status: "added", Staged: true},
				{Path: "newfile.go", StatusCode: "AD", Status: "deleted", Staged: false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGitStatusOutput(tt.input)
			if len(got) != len(tt.expected) {
				t.Fatalf("expected %d entries, got %d: %+v", len(tt.expected), len(got), got)
			}
			for i, want := range tt.expected {
				g := got[i]
				if g.Path != want.Path {
					t.Errorf("[%d] Path: got %q, want %q", i, g.Path, want.Path)
				}
				if g.StatusCode != want.StatusCode {
					t.Errorf("[%d] StatusCode: got %q, want %q", i, g.StatusCode, want.StatusCode)
				}
				if g.Status != want.Status {
					t.Errorf("[%d] Status: got %q, want %q", i, g.Status, want.Status)
				}
				if g.Staged != want.Staged {
					t.Errorf("[%d] Staged: got %v, want %v", i, g.Staged, want.Staged)
				}
			}
		})
	}
}
