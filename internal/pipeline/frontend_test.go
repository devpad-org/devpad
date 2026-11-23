package pipeline

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// The app's types list exactly the values each of the server's enums has,
// so that adding, renaming or dropping one fails here rather than showing
// the app a value it can't name. The app's records keyed by these types
// (PIPELINE_STATUS, JOB_STATUS, GATE_OVERRIDDEN) are then checked for every
// value by its type-check.
func TestEnumsMatchTheFrontend(t *testing.T) {
	const api = "../../frontend/src/api/pipelines.ts"
	for _, tc := range []struct {
		file, ts string
		server   []string
	}{
		{api, "PipelineStatus", names(Statuses())},
		{api, "JobStatus", names(JobStatuses())},
		{api, "PipelineTrigger", names(Triggers())},
		{api, "PipelinePurpose", names(Purposes())},
		{api, "MergeGateState", names(GateStates())},
	} {
		frontend := tsUnion(t, tc.file, tc.ts)
		slices.Sort(frontend)
		slices.Sort(tc.server)
		if !slices.Equal(frontend, tc.server) {
			t.Errorf("the server's %s values are %v, the frontend's %v", tc.ts, tc.server, frontend)
		}
	}
}

// names are an enum's values as strings.
func names[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

// tsUnion reads the string literals of the TypeScript union type name
// exported from file, up to the blank line that ends it.
func tsUnion(t *testing.T, file, name string) []string {
	t.Helper()
	source, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading %s: %v", file, err)
	}
	block := regexp.MustCompile(`(?s)export type ` + name + ` =(.*?)\n\n`).FindSubmatch(source)
	if block == nil {
		t.Fatalf("cannot find %s in %s", name, file)
	}
	var values []string
	for _, m := range regexp.MustCompile(`'([a-z_]*)'`).FindAllSubmatch(block[1], -1) {
		values = append(values, string(m[1]))
	}
	return values
}

// The app's jobFailed names the job statuses JobStatus.Failed does.
func TestJobFailedMatchesTheFrontend(t *testing.T) {
	source, err := os.ReadFile("../../frontend/src/components/pipeline/pipelineFormat.ts")
	if err != nil {
		t.Fatalf("reading the frontend's pipeline format: %v", err)
	}
	body := regexp.MustCompile(`(?s)export function jobFailed\(j: PipelineJob\): boolean \{(.*?)\n\}`).FindSubmatch(source)
	if body == nil {
		t.Fatal("cannot find jobFailed in frontend/src/components/pipeline/pipelineFormat.ts")
	}
	var frontend, server []string
	for _, m := range regexp.MustCompile(`'([a-z_]+)'`).FindAllSubmatch(body[1], -1) {
		frontend = append(frontend, string(m[1]))
	}
	for _, s := range JobStatuses() {
		if s.Failed() {
			server = append(server, string(s))
		}
	}
	slices.Sort(frontend)
	slices.Sort(server)
	if !slices.Equal(frontend, server) {
		t.Errorf("the server's failed job statuses are %v, the frontend's jobFailed names %v", server, frontend)
	}
}
