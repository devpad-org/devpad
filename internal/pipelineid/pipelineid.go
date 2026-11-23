// Package pipelineid is the rule for the ids devpad gives pipelines, which
// name what a pipeline tests in a project's canonical repo and everything
// its jobs make in Docker. The host, its client for the repo container and
// the repo container's agent all check ids with it.
package pipelineid

import "regexp"

// RefPrefix is where a canonical repo keeps what each pipeline tests, as
// refs/devpad/pipelines/<id>.
const RefPrefix = "refs/devpad/pipelines/"

var pattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Valid reports whether id is one a pipeline can go by: lowercase letters,
// digits and hyphens, starting with a letter or digit, at most 64. Such an
// id is safe in a ref and in Docker's names and labels.
func Valid(id string) bool { return pattern.MatchString(id) }

// Ref is the ref of what pipeline id tests.
func Ref(id string) string { return RefPrefix + id }
