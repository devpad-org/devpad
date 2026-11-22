package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/project"
)

// Files are where a commit keeps its pipeline, of which it may have one.
var Files = []string{".devpad/pipeline.yml", ".devpad/pipeline.yaml"}

// Source says where a pipeline's jobs came from.
type Source string

const (
	// SourceFile is a pipeline file in the commit.
	SourceFile Source = "file"
	// SourceProject is the project's pipeline setting, for a commit with no
	// file.
	SourceProject Source = "project"
	// SourceNone is neither: the commit has no pipeline.
	SourceNone Source = "none"
)

// Resolved is the pipeline of a commit.
type Resolved struct {
	Source Source
	// Path is the file the jobs came from, for SourceFile.
	Path string
	// SHA256 is the hash, in hex, of the YAML the jobs came from, or failed
	// to; empty for SourceNone.
	SHA256 string
	// Config is the pipeline, nil when there is none or it is Invalid.
	Config *Config
	// Invalid says why the pipeline could not be read, such as YAML that
	// does not parse. A pipeline that has it fails with it.
	Invalid error
}

// RepoReader reads a project's canonical repo.
type RepoReader interface {
	ReadAt(ctx context.Context, userID, projectID int64, in agent.RepoReadRequest) (*agent.RepoRead, error)
}

var commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// Resolve finds the pipeline at commit, a full commit hash in p's canonical
// repo: the commit's pipeline file, or when it has none the project's
// setting, or else none. A file that does not parse makes the pipeline
// Invalid, and the setting is never used instead. The error is for failing
// to read the repo, which says nothing of the pipeline.
func Resolve(ctx context.Context, repo RepoReader, p *project.Project, commit string) (*Resolved, error) {
	if !commitPattern.MatchString(commit) {
		return nil, fmt.Errorf("resolving a pipeline: %q is not a full commit hash", commit)
	}
	read, err := repo.ReadAt(ctx, p.UserID, p.ID, agent.RepoReadRequest{Rev: commit, Files: Files, MaxBytes: MaxConfigBytes + 1})
	if err != nil {
		return nil, fmt.Errorf("reading the pipeline at %s: %w", commit, err)
	}
	if read.Commit != commit {
		return nil, fmt.Errorf("reading the pipeline at %s: read %s instead", commit, read.Commit)
	}
	var found []agent.RepoReadFile
	for _, f := range read.Files {
		if f.Found {
			found = append(found, f)
		}
	}
	switch len(found) {
	case 0:
		return resolveSetting(p.PipelineConfig), nil
	case 1:
	default:
		return &Resolved{Source: SourceFile, Path: found[0].Path, Invalid: fmt.Errorf("both %s exist: keep one", strings.Join(Files, " and "))}, nil
	}
	f := found[0]
	out := &Resolved{Source: SourceFile, Path: f.Path}
	// A file the agent did not read has no contents to hash, and is over
	// the limit anyway.
	if f.TooLarge || f.Skipped {
		out.Invalid = fmt.Errorf("%s: the pipeline is over the limit of %s", f.Path, sizeOf(MaxConfigBytes))
		return out, nil
	}
	out.SHA256 = hash(f.Content)
	if out.Config, err = Parse(f.Content); err != nil {
		out.Invalid = fmt.Errorf("%s: %w", f.Path, err)
	}
	return out, nil
}

// resolveSetting is the pipeline the project's setting gives a commit with
// no pipeline file.
func resolveSetting(config string) *Resolved {
	if strings.TrimSpace(config) == "" {
		return &Resolved{Source: SourceNone}
	}
	out := &Resolved{Source: SourceProject, SHA256: hash([]byte(config))}
	cfg, err := Parse([]byte(config))
	if err != nil {
		out.Invalid = fmt.Errorf("the project's pipeline: %w", err)
		return out
	}
	out.Config = cfg
	return out
}

// ErrInvalidSetting refuses a project pipeline setting that does not parse.
var ErrInvalidSetting = errors.New("the pipeline does not parse")

// ValidateSetting checks a project's pipeline setting: blank, which means
// none, or YAML that parses.
func ValidateSetting(config string) error {
	if strings.TrimSpace(config) == "" {
		return nil
	}
	if _, err := Parse([]byte(config)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSetting, err)
	}
	return nil
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
