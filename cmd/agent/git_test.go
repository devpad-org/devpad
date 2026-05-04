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

func TestParseGitRemoteOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []gitRemoteEntry
	}{
		{
			name:     "empty output",
			input:    "",
			expected: []gitRemoteEntry{},
		},
		{
			name:  "single remote",
			input: "origin\thttps://github.com/user/repo.git (fetch)\norigin\thttps://github.com/user/repo.git (push)\n",
			expected: []gitRemoteEntry{
				{Name: "origin", FetchURL: "https://github.com/user/repo.git", PushURL: "https://github.com/user/repo.git"},
			},
		},
		{
			name: "multiple remotes",
			input: "origin\thttps://github.com/user/repo.git (fetch)\n" +
				"origin\thttps://github.com/user/repo.git (push)\n" +
				"upstream\thttps://github.com/org/repo.git (fetch)\n" +
				"upstream\thttps://github.com/org/repo.git (push)\n",
			expected: []gitRemoteEntry{
				{Name: "origin", FetchURL: "https://github.com/user/repo.git", PushURL: "https://github.com/user/repo.git"},
				{Name: "upstream", FetchURL: "https://github.com/org/repo.git", PushURL: "https://github.com/org/repo.git"},
			},
		},
		{
			name: "different fetch and push URLs",
			input: "origin\thttps://github.com/user/repo.git (fetch)\n" +
				"origin\tgit@github.com:user/repo.git (push)\n",
			expected: []gitRemoteEntry{
				{Name: "origin", FetchURL: "https://github.com/user/repo.git", PushURL: "git@github.com:user/repo.git"},
			},
		},
		{
			name:  "ssh URL",
			input: "origin\tgit@github.com:user/repo.git (fetch)\norigin\tgit@github.com:user/repo.git (push)\n",
			expected: []gitRemoteEntry{
				{Name: "origin", FetchURL: "git@github.com:user/repo.git", PushURL: "git@github.com:user/repo.git"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGitRemoteOutput(tt.input)
			if len(got) != len(tt.expected) {
				t.Fatalf("expected %d entries, got %d: %+v", len(tt.expected), len(got), got)
			}
			for i, want := range tt.expected {
				g := got[i]
				if g.Name != want.Name {
					t.Errorf("[%d] Name: got %q, want %q", i, g.Name, want.Name)
				}
				if g.FetchURL != want.FetchURL {
					t.Errorf("[%d] FetchURL: got %q, want %q", i, g.FetchURL, want.FetchURL)
				}
				if g.PushURL != want.PushURL {
					t.Errorf("[%d] PushURL: got %q, want %q", i, g.PushURL, want.PushURL)
				}
			}
		})
	}
}

func TestParseGitRemoteNames(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty output",
			input:    "",
			expected: []string{},
		},
		{
			name:     "single remote",
			input:    "origin\n",
			expected: []string{"origin"},
		},
		{
			name:     "multiple remotes",
			input:    "origin\nupstream\nfork\n",
			expected: []string{"origin", "upstream", "fork"},
		},
		{
			name:     "ignores blank lines",
			input:    "\norigin\n\nupstream\n",
			expected: []string{"origin", "upstream"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGitRemoteNames(tt.input)
			if len(got) != len(tt.expected) {
				t.Fatalf("expected %d remotes, got %d: %+v", len(tt.expected), len(got), got)
			}
			for i, want := range tt.expected {
				if got[i] != want {
					t.Errorf("[%d] remote: got %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func TestIsRemoteBranch(t *testing.T) {
	tests := []struct {
		name     string
		branch   string
		remotes  []string
		expected bool
	}{
		{
			name:     "origin branch",
			branch:   "origin/main",
			remotes:  []string{"origin"},
			expected: true,
		},
		{
			name:     "non-origin branch",
			branch:   "upstream/main",
			remotes:  []string{"origin", "upstream"},
			expected: true,
		},
		{
			name:     "local branch matching remote suffix",
			branch:   "main",
			remotes:  []string{"origin"},
			expected: false,
		},
		{
			name:     "unknown prefix",
			branch:   "fork/main",
			remotes:  []string{"origin", "upstream"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRemoteBranch(tt.branch, tt.remotes); got != tt.expected {
				t.Fatalf("isRemoteBranch(%q, %+v) = %v, want %v", tt.branch, tt.remotes, got, tt.expected)
			}
		})
	}
}

func TestGitLogArgs(t *testing.T) {
	tests := []struct {
		name        string
		count       string
		allBranches bool
		expected    []string
	}{
		{
			name:        "current branch log",
			count:       "50",
			allBranches: false,
			expected:    []string{"log", "--topo-order", "--format=%H%n%h%n%P%n%an%n%ae%n%at%n%s", "-n", "50"},
		},
		{
			name:        "all branches log",
			count:       "200",
			allBranches: true,
			expected:    []string{"log", "--topo-order", "--format=%H%n%h%n%P%n%an%n%ae%n%at%n%s", "-n", "200", "--all"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gitLogArgs(tt.count, tt.allBranches)
			if len(got) != len(tt.expected) {
				t.Fatalf("expected %d args, got %d: %+v", len(tt.expected), len(got), got)
			}
			for i, want := range tt.expected {
				if got[i] != want {
					t.Errorf("[%d] arg: got %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func TestParseGitLogOutput(t *testing.T) {
	input := "aaa111\naaa111\nbbb222 ccc333\nAda Lovelace\nada@example.com\n1710000000\nMerge feature\n" +
		"bbb222\nbbb222\n\nGrace Hopper\ngrace@example.com\n1709990000\nInitial commit\n"

	got := parseGitLogOutput(input)
	if len(got) != 2 {
		t.Fatalf("expected 2 commits, got %d: %+v", len(got), got)
	}

	if got[0].Hash != "aaa111" || got[0].ShortHash != "aaa111" || got[0].Message != "Merge feature" {
		t.Fatalf("unexpected first commit: %+v", got[0])
	}
	if len(got[0].Parents) != 2 || got[0].Parents[0] != "bbb222" || got[0].Parents[1] != "ccc333" {
		t.Fatalf("unexpected parents for first commit: %+v", got[0].Parents)
	}
	if len(got[1].Parents) != 0 {
		t.Fatalf("expected root commit to have no parents, got %+v", got[1].Parents)
	}
}
