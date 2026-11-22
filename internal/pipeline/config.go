// Package pipeline is a project's CI pipeline: the jobs its
// .devpad/pipeline.yml names, which run on each new commit of the protected
// branches the user picks and on each merge request into them.
//
// Which copy of the file counts decides what an agent can get away with. A
// merge request's pipeline comes from its target branch, never from the
// change, so an agent cannot rewrite the jobs that grade it. A commit
// without a file falls back to the project's own copy, which only the user
// edits. A file that does not parse fails the pipeline, and never falls
// back, which would quietly run other jobs.
package pipeline

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	// MaxConfigBytes caps a pipeline's YAML.
	MaxConfigBytes = 64 << 10
	// MaxJobs caps the jobs in a pipeline.
	MaxJobs = 20
	// DefaultTimeout is how long a job may run when it does not say.
	DefaultTimeout = 10 * time.Minute
	// MaxTimeout is the longest a job may ask to run.
	MaxTimeout = time.Hour
	// maxJobServices caps the services one job asks for.
	maxJobServices = 8
)

// Config is a pipeline: its jobs, in the order the file lists them.
type Config struct {
	Jobs []Job
}

// Job is one job of a pipeline, which runs in a fresh container of its
// own.
type Job struct {
	// Name is unique within the pipeline.
	Name string
	// Run is the bash script the job runs, which stops at the first command
	// that fails.
	Run string
	// Timeout is how long the job may run before it is stopped and fails.
	Timeout time.Duration
	// Services are the types of the project's services the job gets fresh
	// copies of, such as postgres.
	Services []string
}

var (
	jobNamePattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	serviceTypePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// Parse reads a pipeline's YAML, refusing anything it does not know, so a
// misspelt key fails rather than quietly leaving a job out. Its errors say
// what is wrong and on which line, for the user to fix.
func Parse(data []byte) (*Config, error) {
	if len(data) > MaxConfigBytes {
		return nil, fmt.Errorf("the pipeline is %s, over the limit of %s", sizeOf(len(data)), sizeOf(MaxConfigBytes))
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the pipeline is empty: list its jobs under jobs")
		}
		return nil, syntaxError(err)
	}
	var next yaml.Node
	if err := dec.Decode(&next); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, syntaxError(err)
		}
		return nil, at(&next, "a pipeline is one YAML document, but another starts here")
	}
	if len(doc.Content) != 1 {
		return nil, errors.New("the pipeline is empty: list its jobs under jobs")
	}

	root := deref(doc.Content[0])
	if root.Kind == yaml.ScalarNode && root.ShortTag() == "!!null" {
		return nil, errors.New("the pipeline is empty: list its jobs under jobs")
	}
	fields, err := mapping(root, "the pipeline")
	if err != nil {
		return nil, err
	}
	var jobs *yaml.Node
	for _, f := range fields {
		if f.key != "jobs" {
			return nil, at(f.keyNode, "unknown key %q: a pipeline has only jobs", f.key)
		}
		jobs = f.value
	}
	if jobs == nil {
		return nil, at(root, "the pipeline has no jobs: list them under jobs")
	}
	return parseJobs(jobs)
}

func parseJobs(n *yaml.Node) (*Config, error) {
	if deref(n).ShortTag() == "!!null" {
		return nil, at(n, "jobs is empty: give each job a name and what it runs")
	}
	fields, err := mapping(n, "jobs")
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, at(n, "jobs is empty: give each job a name and what it runs")
	}
	if len(fields) > MaxJobs {
		return nil, at(fields[MaxJobs].keyNode, "a pipeline can have at most %d jobs", MaxJobs)
	}
	cfg := &Config{Jobs: make([]Job, 0, len(fields))}
	for _, f := range fields {
		if !jobNamePattern.MatchString(f.key) {
			return nil, at(f.keyNode, "%q can't name a job: use up to 40 lowercase letters, digits, - and _, starting with a letter or digit", f.key)
		}
		job, err := parseJob(f.key, f.value)
		if err != nil {
			return nil, err
		}
		cfg.Jobs = append(cfg.Jobs, job)
	}
	return cfg, nil
}

func parseJob(name string, n *yaml.Node) (Job, error) {
	what := "job " + name
	fields, err := mapping(n, what)
	if err != nil {
		return Job{}, err
	}
	job := Job{Name: name, Timeout: DefaultTimeout}
	hasRun := false
	for _, f := range fields {
		v := deref(f.value)
		switch f.key {
		case "run":
			if v.Kind != yaml.ScalarNode || v.ShortTag() != "!!str" {
				return Job{}, at(f.value, "%s: run must be the commands the job runs, as text", what)
			}
			if strings.TrimSpace(v.Value) == "" {
				return Job{}, at(f.value, "%s: run is empty", what)
			}
			if strings.ContainsRune(v.Value, 0) {
				return Job{}, at(f.value, "%s: run contains a NUL character", what)
			}
			job.Run, hasRun = v.Value, true
		case "timeout":
			d, err := parseTimeout(v)
			if err != nil {
				return Job{}, at(f.value, "%s: %v", what, err)
			}
			job.Timeout = d
		case "services":
			services, err := parseServices(what, f.value)
			if err != nil {
				return Job{}, err
			}
			job.Services = services
		default:
			return Job{}, at(f.keyNode, "%s: unknown key %q: a job has run, timeout and services", what, f.key)
		}
	}
	if !hasRun {
		return Job{}, at(n, "%s has nothing to run: add run with its commands", what)
	}
	return job, nil
}

func parseTimeout(v *yaml.Node) (time.Duration, error) {
	const want = "timeout must be a duration such as 10m or 1h"
	if v.Kind != yaml.ScalarNode || v.ShortTag() != "!!str" {
		return 0, errors.New(want)
	}
	d, err := time.ParseDuration(strings.TrimSpace(v.Value))
	switch {
	case err != nil:
		return 0, errors.New(want)
	case d <= 0:
		return 0, errors.New("timeout must be longer than nothing")
	case d > MaxTimeout:
		return 0, fmt.Errorf("timeout can be at most %s", formatDuration(MaxTimeout))
	}
	return d, nil
}

func parseServices(what string, n *yaml.Node) ([]string, error) {
	v := deref(n)
	if v.ShortTag() == "!!null" {
		return nil, nil
	}
	if v.Kind != yaml.SequenceNode {
		return nil, at(n, "%s: services must be a list of the project's services, such as [postgres]", what)
	}
	if len(v.Content) > maxJobServices {
		return nil, at(n, "%s: a job can have at most %d services", what, maxJobServices)
	}
	services := make([]string, 0, len(v.Content))
	for _, item := range v.Content {
		s := deref(item)
		if s.Kind != yaml.ScalarNode || !serviceTypePattern.MatchString(s.Value) {
			return nil, at(item, "%s: services lists the project's services by type, such as postgres", what)
		}
		for _, have := range services {
			if have == s.Value {
				return nil, at(item, "%s lists the service %s twice", what, s.Value)
			}
		}
		services = append(services, s.Value)
	}
	return services, nil
}

// field is a key of a YAML mapping and its value.
type field struct {
	key     string
	keyNode *yaml.Node
	value   *yaml.Node
}

// mapping lists a mapping's keys in order, refusing anything else, keys
// that are not plain names, a key given twice and merge keys, whose fields
// would come from somewhere a reader of the job does not see.
func mapping(n *yaml.Node, what string) ([]field, error) {
	m := deref(n)
	if m.Kind != yaml.MappingNode {
		return nil, at(n, "%s must be a mapping of names to values", what)
	}
	fields := make([]field, 0, len(m.Content)/2)
	seen := make(map[string]bool, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := deref(m.Content[i])
		switch {
		case k.Kind != yaml.ScalarNode:
			return nil, at(k, "%s has a key that is not a name", what)
		case k.ShortTag() == "!!merge":
			return nil, at(k, "%s: merge keys (<<) aren't supported; write the fields out", what)
		case seen[k.Value]:
			return nil, at(k, "%s has %q twice", what, k.Value)
		}
		seen[k.Value] = true
		fields = append(fields, field{key: k.Value, keyNode: k, value: m.Content[i+1]})
	}
	return fields, nil
}

// deref follows an alias to the node it names.
func deref(n *yaml.Node) *yaml.Node {
	for i := 0; n.Kind == yaml.AliasNode && n.Alias != nil && i < 16; i++ {
		n = n.Alias
	}
	return n
}

// at is an error about what is on n's line.
func at(n *yaml.Node, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if n.Line > 0 {
		return fmt.Errorf("line %d: %s", n.Line, msg)
	}
	return errors.New(msg)
}

// syntaxError is the YAML parser's error, without its package's name.
func syntaxError(err error) error {
	return errors.New(strings.TrimPrefix(err.Error(), "yaml: "))
}

func sizeOf(n int) string {
	if n < 1<<10 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%d KB", (n+1<<10-1)>>10)
}

// formatDuration writes d as a user would, such as 1h or 1h30m, rather than
// Go's 1h0m0s.
func formatDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
