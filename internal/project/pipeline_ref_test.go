package project

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/pipelineid"
)

func TestPipelineRefsGoToTheRepoContainer(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})
	ctx := context.Background()
	source, target := strings.Repeat("a", 40), strings.Repeat("b", 40)

	pin := agent.PipelinePin{ID: "12", Source: source, Target: target}
	got, err := e.svc.PinPipeline(ctx, e.userID, p.ID, pin)
	if err != nil || got.Ref != pipelineid.Ref("12") {
		t.Fatalf("pin: %+v %v", got, err)
	}
	var bundle bytes.Buffer
	if n, err := e.svc.PipelineBundle(ctx, e.userID, p.ID, "12", &bundle); err != nil || bundle.String() != "bundle-of-pipeline-12" || n != int64(bundle.Len()) {
		t.Fatalf("bundle: %d %v %q", n, err, bundle.String())
	}
	if err := e.svc.DeletePipelineRef(ctx, e.userID, p.ID, "12"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !slices.Equal(e.repoAgent.pins, []agent.PipelinePin{pin}) || !slices.Equal(e.repoAgent.pipelineDeletes, []string{"12"}) {
		t.Fatalf("repo agent saw pins %+v and deletes %v", e.repoAgent.pins, e.repoAgent.pipelineDeletes)
	}

	// What the agent would refuse never reaches it, and another user's
	// project is not found.
	for _, bad := range []agent.PipelinePin{{ID: "x/y", Commit: source}, {ID: "1", Commit: "main"}, {ID: "1"}} {
		if _, err := e.svc.PinPipeline(ctx, e.userID, p.ID, bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%+v: expected invalid input, got %v", bad, err)
		}
	}
	if _, err := e.svc.PipelineBundle(ctx, e.userID, p.ID, "../12", &bundle); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("bundle of a bad id: %v", err)
	}
	if err := e.svc.DeletePipelineRef(ctx, e.userID, p.ID, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("delete of no id: %v", err)
	}
	if len(e.repoAgent.pins) != 1 || len(e.repoAgent.pipelineDeletes) != 1 {
		t.Fatalf("expected nothing more sent, got %+v %v", e.repoAgent.pins, e.repoAgent.pipelineDeletes)
	}
	if _, err := e.svc.PinPipeline(ctx, e.otherID, p.ID, pin); !errors.Is(err, ErrForbidden) {
		t.Fatalf("another user's project: %v", err)
	}
}
