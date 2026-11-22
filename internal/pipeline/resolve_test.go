package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/project"
)

const commit = "0123456789abcdef0123456789abcdef01234567"

// fakeRepo is a commit's files, read the way the repo agent reads them.
type fakeRepo struct {
	files map[string]string
	// tooLarge are files found but over the agent's limit.
	tooLarge map[string]bool
	err      error
	// commit is what the rev resolves to; the rev itself when empty.
	commit string
	asked  []agent.RepoReadRequest
}

func (f *fakeRepo) ReadAt(_ context.Context, _, _ int64, in agent.RepoReadRequest) (*agent.RepoRead, error) {
	f.asked = append(f.asked, in)
	if f.err != nil {
		return nil, f.err
	}
	out := &agent.RepoRead{Commit: in.Rev}
	if f.commit != "" {
		out.Commit = f.commit
	}
	for _, p := range in.Files {
		content, ok := f.files[p]
		file := agent.RepoReadFile{Path: p, Found: ok || f.tooLarge[p], TooLarge: f.tooLarge[p]}
		if ok {
			file.Content = []byte(content)
		}
		out.Files = append(out.Files, file)
	}
	return out, nil
}

const validYAML = "jobs:\n  test:\n    run: make test\n"

func TestResolveFindsTheCommitsPipeline(t *testing.T) {
	ctx := context.Background()
	other := "jobs:\n  lint:\n    run: make lint\n"
	tests := []struct {
		name    string
		files   map[string]string
		setting string
		source  Source
		path    string
		// hashOf is the YAML the jobs came from.
		hashOf string
		job    string
		// invalid is in the reason the pipeline cannot run, if it can't.
		invalid string
	}{
		{name: "file", files: map[string]string{".devpad/pipeline.yml": validYAML}, setting: other, source: SourceFile, path: ".devpad/pipeline.yml", hashOf: validYAML, job: "test"},
		{name: "yaml extension", files: map[string]string{".devpad/pipeline.yaml": validYAML}, source: SourceFile, path: ".devpad/pipeline.yaml", hashOf: validYAML, job: "test"},
		{name: "setting", setting: other, source: SourceProject, hashOf: other, job: "lint"},
		{name: "none", setting: " \n", source: SourceNone},
		{name: "file that doesn't parse never falls back", files: map[string]string{".devpad/pipeline.yml": "jobs:\n  test:\n    script: make\n"}, setting: other, source: SourceFile, path: ".devpad/pipeline.yml", hashOf: "jobs:\n  test:\n    script: make\n", invalid: `.devpad/pipeline.yml: line 3: job test: unknown key "script"`},
		{name: "both files", files: map[string]string{".devpad/pipeline.yml": validYAML, ".devpad/pipeline.yaml": other}, source: SourceFile, path: ".devpad/pipeline.yml", invalid: "both .devpad/pipeline.yml and .devpad/pipeline.yaml exist"},
		{name: "setting that doesn't parse", setting: "jobs: {}\n", source: SourceProject, hashOf: "jobs: {}\n", invalid: "the project's pipeline: line 1: jobs is empty"},
		{name: "file over the limit", files: map[string]string{".devpad/pipeline.yml": "jobs:\n  a:\n    run: " + strings.Repeat("x", MaxConfigBytes) + "\n"}, source: SourceFile, path: ".devpad/pipeline.yml", hashOf: "jobs:\n  a:\n    run: " + strings.Repeat("x", MaxConfigBytes) + "\n", invalid: "over the limit of 64 KB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{files: tt.files}
			p := &project.Project{ID: 7, UserID: 3, PipelineConfig: tt.setting}
			got, err := Resolve(ctx, repo, p, commit)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			want := []agent.RepoReadRequest{{Rev: commit, Files: Files, MaxBytes: MaxConfigBytes + 1}}
			if !reflect.DeepEqual(repo.asked, want) {
				t.Fatalf("asked %+v, want %+v", repo.asked, want)
			}
			if got.Source != tt.source || got.Path != tt.path {
				t.Fatalf("source %s %q, want %s %q", got.Source, got.Path, tt.source, tt.path)
			}
			wantHash := ""
			if tt.hashOf != "" {
				wantHash = hash([]byte(tt.hashOf))
			}
			if got.SHA256 != wantHash {
				t.Fatalf("hash %q, want %q", got.SHA256, wantHash)
			}
			if tt.invalid != "" {
				if got.Invalid == nil || !strings.Contains(got.Invalid.Error(), tt.invalid) || got.Config != nil {
					t.Fatalf("expected invalid %q and no config, got %v, %+v", tt.invalid, got.Invalid, got.Config)
				}
				return
			}
			if got.Invalid != nil {
				t.Fatalf("unexpected invalid: %v", got.Invalid)
			}
			if tt.job == "" {
				if got.Config != nil {
					t.Fatalf("expected no pipeline, got %+v", got.Config)
				}
				return
			}
			if got.Config == nil || len(got.Config.Jobs) != 1 || got.Config.Jobs[0].Name != tt.job {
				t.Fatalf("expected job %s, got %+v", tt.job, got.Config)
			}
		})
	}
}

// A file the agent refuses to read for its size is invalid, not missing.
func TestResolveRefusesAFileTooLargeToRead(t *testing.T) {
	repo := &fakeRepo{tooLarge: map[string]bool{".devpad/pipeline.yml": true}}
	got, err := Resolve(context.Background(), repo, &project.Project{PipelineConfig: validYAML}, commit)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Source != SourceFile || got.Invalid == nil || got.SHA256 != "" {
		t.Fatalf("expected an invalid file, got %+v", got)
	}
}

// Resolving reads only at a commit, so what a moving branch held when the
// pipeline was queued can never be mistaken for what it holds later, and
// failing to read is not a pipeline that doesn't parse.
func TestResolveReadsOnlyAtACommit(t *testing.T) {
	ctx := context.Background()
	p := &project.Project{}
	for _, rev := range []string{"main", commit[:12], strings.ToUpper(commit), ""} {
		repo := &fakeRepo{}
		if _, err := Resolve(ctx, repo, p, rev); err == nil || len(repo.asked) != 0 {
			t.Fatalf("rev %q: expected a refusal without reading, got %v", rev, err)
		}
	}
	sha256Commit := strings.Repeat("ab", 32)
	if _, err := Resolve(ctx, &fakeRepo{}, p, sha256Commit); err != nil {
		t.Fatalf("a SHA-256 commit: %v", err)
	}
	readErr := errors.New("repo container stopped")
	if _, err := Resolve(ctx, &fakeRepo{err: readErr}, p, commit); !errors.Is(err, readErr) {
		t.Fatalf("expected the read error, got %v", err)
	}
	if _, err := Resolve(ctx, &fakeRepo{commit: strings.Repeat("1", 40)}, p, commit); err == nil {
		t.Fatal("expected a read at another commit refused")
	}
}

func TestValidateSetting(t *testing.T) {
	for _, ok := range []string{"", "  \n", validYAML} {
		if err := ValidateSetting(ok); err != nil {
			t.Fatalf("%q: %v", ok, err)
		}
	}
	err := ValidateSetting("jobs:\n  a:\n    run: make\n    timeout: 2h\n")
	if !errors.Is(err, ErrInvalidSetting) || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("expected ErrInvalidSetting naming line 4, got %v", err)
	}
}

type fakeSettings struct {
	config string
	err    error
	calls  int
}

func (f *fakeSettings) SetPipelineConfig(_ context.Context, userID, projectID int64, config string) (*project.Project, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	f.config = config
	return &project.Project{ID: projectID, UserID: userID, PipelineConfig: config}, nil
}

func TestHandleSetConfig(t *testing.T) {
	put := func(settings *fakeSettings, body string) (int, map[string]string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/projects/7/pipeline", strings.NewReader(body))
		req.SetPathValue("id", "7")
		req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: 3}))
		rec := httptest.NewRecorder()
		NewHandler(settings).HandleSetConfig(rec, req)
		var out map[string]string
		json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&out)
		return rec.Code, out
	}
	body := func(config string) string {
		b, _ := json.Marshal(map[string]string{"config": config})
		return string(b)
	}

	settings := &fakeSettings{}
	if code, out := put(settings, body(validYAML)); code != http.StatusOK || out["config"] != validYAML || settings.config != validYAML {
		t.Fatalf("valid: %d %v, stored %q", code, out, settings.config)
	}
	if code, out := put(settings, body("jobs:\n  a:\n    scrpit: make\n")); code != http.StatusBadRequest || !strings.Contains(out["error"], `line 3: job a: unknown key "scrpit"`) || settings.calls != 1 {
		t.Fatalf("invalid: %d %v after %d calls", code, out, settings.calls)
	}
	// Blank clears it.
	if code, _ := put(settings, body(" \n\t")); code != http.StatusOK || settings.config != "" {
		t.Fatalf("blank: %d, stored %q", code, settings.config)
	}
	if code, _ := put(&fakeSettings{err: project.ErrForbidden}, body(validYAML)); code != http.StatusForbidden {
		t.Fatalf("someone else's project: %d", code)
	}
	if code, _ := put(&fakeSettings{err: project.ErrNotFound}, body(validYAML)); code != http.StatusNotFound {
		t.Fatalf("missing project: %d", code)
	}
	if code, _ := put(&fakeSettings{}, "not json"); code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", code)
	}
}
