package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPipelinePinValidate(t *testing.T) {
	sha1 := strings.Repeat("a", 40)
	sha256 := strings.Repeat("b", 64)
	for _, ok := range []PipelinePin{
		{ID: "1", Commit: sha1},
		{ID: "pipeline-42", Source: sha1, Target: sha256},
	} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
	for _, bad := range []PipelinePin{
		{ID: "", Commit: sha1},
		{ID: "A", Commit: sha1},
		{ID: "1/2", Commit: sha1},
		{ID: "1"},
		{ID: "1", Commit: sha1, Source: sha1, Target: sha1},
		{ID: "1", Source: sha1},
		{ID: "1", Commit: "main"},
		{ID: "1", Commit: sha1[:39]},
		{ID: "1", Commit: strings.ToUpper(sha1)},
		{ID: "1", Source: sha1, Target: "--all"},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v: expected it refused", bad)
		}
	}
}

func TestRepoPipelineBundleIsCapped(t *testing.T) {
	limit := bundleLimit
	bundleLimit = 10
	t.Cleanup(func() { bundleLimit = limit })
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/repo/pipeline-bundle" || r.URL.Query().Get("id") != "9" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: srv.Client()}

	body = "0123456789"
	var out bytes.Buffer
	if n, err := c.RepoPipelineBundle(context.Background(), "9", &out); err != nil || n != 10 || out.String() != body {
		t.Fatalf("at the limit: %d %v %q", n, err, out.String())
	}
	body = "0123456789x"
	out.Reset()
	if _, err := c.RepoPipelineBundle(context.Background(), "9", &out); !errors.Is(err, ErrBundleTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := c.RepoPipelineBundle(context.Background(), "../9", &out); err == nil {
		t.Fatal("expected a bad id refused")
	}
}

func TestRepoPinPipelineChecksTheAnswer(t *testing.T) {
	var answer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(answer))
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: srv.Client()}
	pin := PipelinePin{ID: "3", Source: strings.Repeat("a", 40), Target: strings.Repeat("b", 40)}

	answer = `{"conflict":{"message":"the source conflicts with the target","paths":["a.txt"]}}`
	got, err := c.RepoPinPipeline(context.Background(), pin)
	if err != nil || got.Conflict == nil || got.Conflict.Paths[0] != "a.txt" {
		t.Fatalf("expected a conflict, not an error: %+v %v", got, err)
	}
	// However many files the agent names, and however long, the client
	// keeps a bounded list, for the pin and the trial merge alike.
	paths := make([]string, MaxConflictPaths+7)
	for i := range paths {
		paths[i] = fmt.Sprintf("dir/%d.txt", i)
	}
	paths[0] = strings.Repeat("é", MaxConflictPathBytes)
	many, _ := json.Marshal(map[string]any{"conflict": map[string]any{"message": "conflicts", "paths": paths}})
	answer = string(many)
	got, err = c.RepoPinPipeline(context.Background(), pin)
	if err != nil || len(got.Conflict.Paths) != MaxConflictPaths || got.Conflict.More != 7 || got.Conflict.Paths[1] != "dir/1.txt" {
		t.Fatalf("a long conflict: %d paths, %d more %v", len(got.Conflict.Paths), got.Conflict.More, err)
	}
	if p := got.Conflict.Paths[0]; len(p) > MaxConflictPathBytes || !strings.HasSuffix(p, "…") || !utf8.ValidString(p) {
		t.Fatalf("a long path kept as %d bytes", len(p))
	}
	merge, err := c.RepoTrialMerge(context.Background(), pin.Source, pin.Target)
	if err != nil || len(merge.Conflict.Paths) != MaxConflictPaths || merge.Conflict.More != 7 {
		t.Fatalf("a long trial merge conflict: %+v %v", merge, err)
	}
	for _, wrong := range []string{
		`{"ref":"refs/devpad/pipelines/4","sha":"` + strings.Repeat("c", 40) + `"}`,
		`{"ref":"refs/devpad/pipelines/3","sha":"main"}`,
		`{}`,
	} {
		answer = wrong
		if _, err := c.RepoPinPipeline(context.Background(), pin); err == nil {
			t.Errorf("%s: expected it refused", wrong)
		}
	}
}
