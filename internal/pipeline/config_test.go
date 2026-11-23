package pipeline

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseReadsJobsInOrder(t *testing.T) {
	cfg, err := Parse([]byte(`# .devpad/pipeline.yml
jobs:
  backend:
    run: go test ./...
    timeout: 15m
    services: [postgres, mariadb]
  frontend:
    run: |
      cd frontend
      npm ci
      npm run type-check
  lint:
    run: make lint
    services: []
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []Job{
		{Name: "backend", Run: "go test ./...", Timeout: 15 * time.Minute, Services: []string{"postgres", "mariadb"}},
		{Name: "frontend", Run: "cd frontend\nnpm ci\nnpm run type-check\n", Timeout: DefaultTimeout},
		{Name: "lint", Run: "make lint", Timeout: DefaultTimeout},
	}
	if !reflect.DeepEqual(cfg.Jobs, want) {
		t.Fatalf("jobs = %#v, want %#v", cfg.Jobs, want)
	}
}

// An anchor lets jobs share a script, and an alias reads as what it names.
func TestParseFollowsAliases(t *testing.T) {
	cfg, err := Parse([]byte(`jobs:
  unit:
    run: &test make test
    services: &db [postgres]
  integration:
    run: *test
    services: *db
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.Jobs[1]; got.Run != "make test" || len(got.Services) != 1 || got.Services[0] != "postgres" {
		t.Fatalf("integration = %#v", got)
	}
}

func TestParseRefusesWhatItDoesNotKnow(t *testing.T) {
	manyJobs := "jobs:\n"
	for i := range MaxJobs + 1 {
		manyJobs += fmt.Sprintf("  job%d:\n    run: make\n", i)
	}
	tests := []struct {
		name string
		yaml string
		// want is in the error, which says what is wrong and where.
		want string
	}{
		{"empty", "", "the pipeline is empty"},
		{"only comments", "# nothing yet\n", "the pipeline is empty"},
		{"null", "~\n", "the pipeline is empty"},
		{"not a mapping", "- run: make\n", "line 1: the pipeline must be a mapping"},
		{"no jobs", "jobs:\n", "jobs is empty"},
		{"no jobs key", "{}\n", "has no jobs"},
		{"empty jobs", "jobs: {}\n", "jobs is empty"},
		{"jobs as a list", "jobs:\n  - run: make\n", "line 2: jobs must be a mapping"},
		{"unknown top-level key", "stages: [test]\njobs:\n  a:\n    run: make\n", `line 1: unknown key "stages"`},
		{"unknown job key", "jobs:\n  a:\n    script: make\n", `line 3: job a: unknown key "script": a job has run, timeout and services`},
		{"no run", "jobs:\n  a:\n    timeout: 5m\n", "line 3: job a has nothing to run"},
		{"empty run", "jobs:\n  a:\n    run: \"  \"\n", "line 3: job a: run is empty"},
		{"run as a number", "jobs:\n  a:\n    run: 5\n", "line 3: job a: run must be the commands"},
		{"run as a list", "jobs:\n  a:\n    run:\n      - make\n", "line 4: job a: run must be the commands"},
		{"run with a NUL", "jobs:\n  a:\n    run: \"make\\0\"\n", "NUL"},
		{"job as text", "jobs:\n  a: make\n", "line 2: job a must be a mapping"},
		{"timeout in seconds", "jobs:\n  a:\n    run: make\n    timeout: 600\n", "line 4: job a: timeout must be a duration"},
		{"timeout nonsense", "jobs:\n  a:\n    run: make\n    timeout: soon\n", "timeout must be a duration"},
		{"timeout zero", "jobs:\n  a:\n    run: make\n    timeout: 0s\n", "timeout must be at least 1s"},
		{"timeout under a second", "jobs:\n  a:\n    run: make\n    timeout: 999ms\n", "line 4: job a: timeout must be at least 1s"},
		{"timeout negative", "jobs:\n  a:\n    run: make\n    timeout: -5m\n", "timeout must be at least 1s"},
		{"timeout too long", "jobs:\n  a:\n    run: make\n    timeout: 61m\n", "timeout can be at most 1h"},
		{"services as text", "jobs:\n  a:\n    run: make\n    services: postgres\n", "line 4: job a: services must be a list"},
		{"service twice", "jobs:\n  a:\n    run: make\n    services: [postgres, postgres]\n", `lists the service "postgres" twice`},
		{"service as null", "jobs:\n  a:\n    run: make\n    services: [null]\n", "line 4: job a: services lists the project's services by type"},
		{"service as a bool", "jobs:\n  a:\n    run: make\n    services: [true]\n", "services lists the project's services by type"},
		{"service with a custom tag", "jobs:\n  a:\n    run: make\n    services: [!db postgres]\n", "services lists the project's services by type"},
		{"service with a bad name", "jobs:\n  a:\n    run: make\n    services: [Postgres]\n", "services lists the project's services by type"},
		{"service as a mapping", "jobs:\n  a:\n    run: make\n    services: [{postgres: 16}]\n", "services lists the project's services by type"},
		{"too many services", "jobs:\n  a:\n    run: make\n    services: [a, b, c, d, e, f, g, h, i]\n", "at most 8 services"},
		{"capital job name", "jobs:\n  Backend:\n    run: make\n", `line 2: "Backend" can't name a job`},
		{"job name with a space", "jobs:\n  my job:\n    run: make\n", "can't name a job"},
		{"job name with a dot", "jobs:\n  a.b:\n    run: make\n", "can't name a job"},
		{"long job name", "jobs:\n  " + strings.Repeat("a", 41) + ":\n    run: make\n", "can't name a job"},
		{"job twice", "jobs:\n  a:\n    run: make\n  a:\n    run: make test\n", `line 4: jobs has "a" twice`},
		{"key twice in a job", "jobs:\n  a:\n    run: make\n    run: make test\n", `line 4: job a has "run" twice`},
		{"job named true", "jobs:\n  true:\n    run: make\n", `line 2: jobs has a key that is not a name: "true" (quote it`},
		{"job named null", "jobs:\n  null:\n    run: make\n", "jobs has a key that is not a name"},
		{"job named with a float", "jobs:\n  1.5:\n    run: make\n", "jobs has a key that is not a name"},
		{"key with a custom tag", "!top jobs:\n  a:\n    run: make\n", `line 1: the pipeline has a key that is not a name: "jobs"`},
		{"key with a binary tag", "jobs:\n  a:\n    !!binary cnVu: make\n", "job a has a key that is not a name"},
		{"merge key", "jobs:\n  a: &a\n    run: make\n  b:\n    <<: *a\n", "merge keys (<<) aren't supported"},
		{"two documents", "jobs:\n  a:\n    run: make\n---\njobs: {}\n", "line 4: a pipeline is one YAML document"},
		{"too many jobs", manyJobs, fmt.Sprintf("at most %d jobs", MaxJobs)},
		// The YAML parser says where, as best it can.
		{"bad YAML", "jobs:\n  a:\n    run: make\n   timeout: 5m\n", "line 1: did not find expected key"},
		{"unclosed list", "jobs:\n  a:\n    run: make\n    services: [postgres\n", "did not find expected ',' or ']'"},
		{"too large", "jobs:\n  a:\n    run: " + strings.Repeat("x", MaxConfigBytes) + "\n", "over the limit of 64 KB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("expected an error, got %#v", cfg)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not say %q", err, tt.want)
			}
			if strings.HasPrefix(err.Error(), "yaml:") {
				t.Fatalf("error %q names the YAML package", err)
			}
		})
	}
}

func TestParseTakesTimeoutsUpToAnHour(t *testing.T) {
	for in, want := range map[string]time.Duration{"1s": time.Second, "30s": 30 * time.Second, "1h": time.Hour, "1h0m": time.Hour, " 45m ": 45 * time.Minute} {
		cfg, err := Parse(fmt.Appendf(nil, "jobs:\n  a:\n    run: make\n    timeout: %q\n", in))
		if err != nil {
			t.Fatalf("timeout %q: %v", in, err)
		}
		if got := cfg.Jobs[0].Timeout; got != want {
			t.Fatalf("timeout %q = %s, want %s", in, got, want)
		}
	}
}

// Job names that YAML reads as numbers still name jobs, as quoted ones do.
func TestParseTakesNumericJobNames(t *testing.T) {
	cfg, err := Parse([]byte("jobs:\n  2024:\n    run: make\n  \"true\":\n    run: make\n  !!str null:\n    run: make\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var names []string
	for _, j := range cfg.Jobs {
		names = append(names, j.Name)
	}
	if strings.Join(names, ",") != "2024,true,null" {
		t.Fatalf("names %v", names)
	}
}

// However a file says a job has no services, the job's Services are the
// same, and so encode the same.
func TestParseNormalisesNoServices(t *testing.T) {
	for _, services := range []string{"", "    services: []\n", "    services: ~\n", "    services:\n"} {
		cfg, err := Parse([]byte("jobs:\n  a:\n    run: make\n" + services))
		if err != nil {
			t.Fatalf("%q: %v", services, err)
		}
		if got := cfg.Jobs[0].Services; got != nil {
			t.Fatalf("%q: services %#v, want nil", services, got)
		}
		if b, _ := json.Marshal(cfg.Jobs[0].Services); string(b) != "null" {
			t.Fatalf("%q: encodes as %s", services, b)
		}
	}
}

// Every bit of the file an error quotes is cut short, since a key can be as
// long as the file.
func TestParseErrorsQuoteLittleOfTheFile(t *testing.T) {
	long := strings.Repeat("k", 60000)
	for name, yaml := range map[string]string{
		"top-level key": "? " + long + "\n: 1\n",
		"job name":      "jobs:\n  ? " + long + "\n  : {run: make}\n",
		"job key":       "jobs:\n  a:\n    run: make\n    ? " + long + "\n    : 1\n",
		"key twice":     "jobs:\n  a:\n    run: make\n  ? \"" + long[:30000] + "\"\n  : 1\n  ? \"" + long[:30000] + "\"\n  : 2\n",
		"tagged key":    "jobs:\n  a:\n    run: make\n    ? !x " + long + "\n    : 1\n",
	} {
		_, err := Parse([]byte(yaml))
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if len(err.Error()) > 300 || !strings.Contains(err.Error(), strings.Repeat("k", maxQuoted)+`"…`) {
			t.Fatalf("%s: error of %d bytes: %.300s", name, len(err.Error()), err)
		}
	}
	if got := quote("a\x1b\u202e"); got != `"a\x1b\u202e"` {
		t.Fatalf("quote escapes nothing: %s", got)
	}
}

// The file is the hostile agent's to write: whatever its shape, parsing it
// takes no more than its size. Aliases are never expanded, nesting is
// bounded by the parser and keys by the size limit.
func TestParseRefusesHostileStructureFast(t *testing.T) {
	// Each level aliases the one before ten times: expanded, the last is
	// 10^12 strings.
	bomb := "jobs:\n  a:\n    run: make\n    services:\n      - &l0 [x, x, x, x, x, x, x, x, x, x]\n"
	for i := 1; i < 12; i++ {
		bomb += fmt.Sprintf("      - &l%d [%s*l%d]\n", i, strings.Repeat(fmt.Sprintf("*l%d, ", i-1), 9), i-1)
	}
	// The same, as the value of a job's unknown key.
	bombKey := strings.Replace(bomb, "    services:\n", "    extra:\n", 1)
	manyKeys := "jobs:\n"
	for i := 0; len(manyKeys) < MaxConfigBytes-100; i++ {
		manyKeys += fmt.Sprintf("  j%d: 1\n", i)
	}
	manyJobKeys := "jobs:\n  a:\n    run: make\n"
	for i := 0; len(manyJobKeys) < MaxConfigBytes-100; i++ {
		manyJobKeys += fmt.Sprintf("    k%d: 1\n", i)
	}
	depth := (MaxConfigBytes - 20) / 2
	tests := []struct{ name, yaml, want string }{
		{"alias bomb", bomb, "at most 8 services"},
		{"alias bomb under an unknown key", bombKey, `unknown key "extra"`},
		{"deep flow nesting", "jobs: " + strings.Repeat("[", depth) + strings.Repeat("]", depth), "exceeded max depth"},
		{"deep mapping nesting", "jobs: " + strings.Repeat("{a: ", (MaxConfigBytes-20)/5) + "1" + strings.Repeat("}", (MaxConfigBytes-20)/5), "exceeded max depth"},
		{"many jobs", manyKeys, fmt.Sprintf("at most %d jobs", MaxJobs)},
		{"many keys in a job", manyJobKeys, `unknown key "k0"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.yaml) > MaxConfigBytes {
				t.Fatalf("test input of %d bytes is over the limit", len(tt.yaml))
			}
			start := time.Now()
			_, err := Parse([]byte(tt.yaml))
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("took %s", elapsed)
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %v does not say %q", err, tt.want)
			}
		})
	}
}
