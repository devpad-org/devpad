package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/project"
)

// Files are where a commit keeps its pipeline, of which it may have one.
func Files() []string {
	return project.PipelineFiles()
}

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
	// Hash tells pipelines apart, in hex. For a pipeline that parses it is
	// JobsHash of its jobs, so editing a comment or the layout of the YAML
	// does not change it. For an Invalid one it is the SHA-256 of the YAML
	// that failed to parse, so a broken file that changes still differs.
	// It is empty only when there is no YAML to hash: SourceNone, both
	// pipeline files at once, or a file too large to read.
	Hash string
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

// Resolve finds the pipeline at commit, a full commit hash in p's canonical
// repo: the commit's pipeline file, or when it has none the project's
// setting, or else none. A file that does not parse makes the pipeline
// Invalid, and the setting is never used instead. The error is for failing
// to read the repo, which says nothing of the pipeline.
func Resolve(ctx context.Context, repo RepoReader, p *project.Project, commit string) (*Resolved, error) {
	if !project.IsCommitHash(commit) {
		return nil, fmt.Errorf("resolving a pipeline: %q is not a full commit hash", commit)
	}
	// The agent's byte budget is checked before each file, never within
	// one: the first file found is read whole (one over the agent's own
	// per-file limit comes back TooLarge, unread), and a later one only
	// while less than MaxBytes has been read. A budget of one byte so reads
	// at most one file, and a second is still reported Found, which is all
	// it takes to refuse having both. Parse refuses a file over
	// MaxConfigBytes.
	read, err := repo.ReadAt(ctx, p.UserID, p.ID, agent.RepoReadRequest{Rev: commit, Files: Files(), MaxBytes: 1})
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
		return &Resolved{Source: SourceFile, Path: found[0].Path, Invalid: fmt.Errorf("both %s exist: keep one", strings.Join(Files(), " and "))}, nil
	}
	f := found[0]
	out := &Resolved{Source: SourceFile, Path: f.Path}
	// The only file found is the first read, so it is never Skipped; one
	// too large for the agent to read has no contents to hash.
	if f.TooLarge {
		out.Invalid = fmt.Errorf("%s: the pipeline is too large to read: it can be at most %s", f.Path, sizeOf(MaxConfigBytes))
		return out, nil
	}
	parse(out, f.Content, f.Path)
	return out, nil
}

// resolveSetting is the pipeline the project's setting gives a commit with
// no pipeline file.
func resolveSetting(config string) *Resolved {
	if strings.TrimSpace(config) == "" {
		return &Resolved{Source: SourceNone}
	}
	out := &Resolved{Source: SourceProject}
	parse(out, []byte(config), "the project's pipeline")
	return out
}

// parse fills in out's Config and Hash from data, or makes it Invalid with
// the parse error, which names where the YAML came from.
func parse(out *Resolved, data []byte, from string) {
	cfg, err := Parse(data)
	if err != nil {
		out.Hash, out.Invalid = hash(data), fmt.Errorf("%s: %w", from, err)
		return
	}
	out.Config, out.Hash = cfg, JobsHash(cfg.Jobs)
}

// JobsHash is the SHA-256, in hex, of a fixed encoding of jobs, in their
// order: what each runs, for how long and with which services, and nothing
// of how the YAML was written.
func JobsHash(jobs []Job) string {
	type canonicalJob struct {
		Name     string   `json:"name"`
		Run      string   `json:"run"`
		Timeout  int64    `json:"timeoutNanos"`
		Services []string `json:"services"`
	}
	type canonical struct {
		Version int            `json:"version"`
		Jobs    []canonicalJob `json:"jobs"`
	}
	c := canonical{Version: 1, Jobs: make([]canonicalJob, 0, len(jobs))}
	for _, j := range jobs {
		services := j.Services
		if services == nil {
			services = []string{}
		}
		c.Jobs = append(c.Jobs, canonicalJob{Name: j.Name, Run: j.Run, Timeout: int64(j.Timeout), Services: services})
	}
	data, err := json.Marshal(c)
	if err != nil {
		// Strings, integers and lists of them always encode.
		panic(fmt.Sprintf("encoding pipeline jobs: %v", err))
	}
	return hash(data)
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
