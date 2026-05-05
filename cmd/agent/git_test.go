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

func TestParseSSHHostKeyPrompt(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantHost    string
		wantKeyType string
		wantFP      string
		wantPrompt  bool
	}{
		{
			name: "standard host",
			output: "The authenticity of host 'git.example.com (203.0.113.10)' can't be established.\n" +
				"ED25519 key fingerprint is SHA256:abc123/def456.\n" +
				"Are you sure you want to continue connecting (yes/no/[fingerprint])?",
			wantHost:    "git.example.com",
			wantKeyType: "ED25519",
			wantFP:      "SHA256:abc123/def456",
			wantPrompt:  true,
		},
		{
			name: "custom port host",
			output: "The authenticity of host '[git.example.com]:2222 ([203.0.113.10]:2222)' can't be established.\n" +
				"ECDSA key fingerprint is SHA256:xyz789.\n",
			wantHost:    "[git.example.com]:2222",
			wantKeyType: "ECDSA",
			wantFP:      "SHA256:xyz789",
			wantPrompt:  true,
		},
		{
			name:       "unrelated git failure",
			output:     "fatal: not a git repository",
			wantPrompt: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSSHHostKeyPrompt(tt.output)
			if !tt.wantPrompt {
				if got != nil {
					t.Fatalf("expected no prompt, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected prompt, got nil")
			}
			if got.Host != tt.wantHost || got.KeyType != tt.wantKeyType || got.Fingerprint != tt.wantFP {
				t.Fatalf("unexpected prompt: %+v", got)
			}
			if got.RawOutput == "" {
				t.Fatal("expected raw output to be preserved")
			}
		})
	}
}

func TestParseSSHHostTarget(t *testing.T) {
	tests := []struct {
		name      string
		host      string
		scanHost  string
		port      string
		knownHost string
		wantErr   bool
	}{
		{
			name:      "plain host",
			host:      "git.example.com",
			scanHost:  "git.example.com",
			knownHost: "git.example.com",
		},
		{
			name:      "custom port",
			host:      "[git.example.com]:2222",
			scanHost:  "git.example.com",
			port:      "2222",
			knownHost: "[git.example.com]:2222",
		},
		{name: "flag injection", host: "-oProxyCommand=bad", wantErr: true},
		{name: "space", host: "git example.com", wantErr: true},
		{name: "bad port", host: "[git.example.com]:99999", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSSHHostTarget(tt.host)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got.ScanHost != tt.scanHost || got.Port != tt.port || got.KnownHost != tt.knownHost {
				t.Fatalf("unexpected target: %+v", got)
			}
		})
	}
}

func TestValidateGitCommitHash(t *testing.T) {
	tests := []struct {
		name    string
		hash    string
		wantErr bool
	}{
		{name: "short sha", hash: "abc1234", wantErr: false},
		{name: "full sha", hash: "0123456789abcdef0123456789abcdef01234567", wantErr: false},
		{name: "empty", hash: "", wantErr: true},
		{name: "too short", hash: "abc123", wantErr: true},
		{name: "flag-like", hash: "-abc1234", wantErr: true},
		{name: "non hex", hash: "abc123g", wantErr: true},
		{name: "too long", hash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGitCommitHash(tt.hash)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestParseGitCommitFilesOutput(t *testing.T) {
	input := "M\tREADME.md\n" +
		"A\tnew.go\n" +
		"D\told.go\n" +
		"R100\told/name.go\tnew/name.go\n" +
		"C075\tsrc/base.go\tsrc/copy.go\n"

	got := parseGitCommitFilesOutput(input)
	expected := []gitCommitFileEntry{
		{Path: "README.md", Status: "modified"},
		{Path: "new.go", Status: "added"},
		{Path: "old.go", Status: "deleted"},
		{Path: "new/name.go", OldPath: "old/name.go", Status: "renamed"},
		{Path: "src/copy.go", OldPath: "src/base.go", Status: "copied"},
	}

	if len(got) != len(expected) {
		t.Fatalf("expected %d files, got %d: %+v", len(expected), len(got), got)
	}
	for i, want := range expected {
		if got[i].Path != want.Path {
			t.Errorf("[%d] Path: got %q, want %q", i, got[i].Path, want.Path)
		}
		if got[i].OldPath != want.OldPath {
			t.Errorf("[%d] OldPath: got %q, want %q", i, got[i].OldPath, want.OldPath)
		}
		if got[i].Status != want.Status {
			t.Errorf("[%d] Status: got %q, want %q", i, got[i].Status, want.Status)
		}
	}
}
