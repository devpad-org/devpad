package mergerequest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"

	"github.com/devpad-org/devpad/internal/agent"
)

// Working out and keeping the look-closely mark (lookclosely.go). The mark
// is worked out from the change of the merge request's head since it left
// its target (merge base to head) and the scripts of its target's pipeline
// jobs: whenever the head moves, whenever its diff is read, and as devpad
// starts. The merge request keeps only its summary, how many files it
// lists for each reason, with the head it is of, so the list of merge
// requests shows it without reading the repo, and never with the change's
// paths; the files themselves come with the diff.

// encodeLookClosely is a mark's summary as its column holds it.
func encodeLookClosely(mark LookCloselySummary) (string, error) {
	if mark.Reasons == nil {
		mark.Reasons = []ReasonCount{}
	}
	b, err := json.Marshal(mark)
	if err != nil {
		return "", fmt.Errorf("encoding the look-closely mark: %w", err)
	}
	return string(b), nil
}

// decodeLookClosely reads a mark's summary from its column, nil when none
// is there or what is there doesn't read as one.
func decodeLookClosely(data string) (*LookCloselySummary, error) {
	if data == "" {
		return nil, nil
	}
	var mark LookCloselySummary
	if err := json.Unmarshal([]byte(data), &mark); err != nil {
		return nil, fmt.Errorf("decoding the look-closely mark: %w", err)
	}
	if mark.Reasons == nil {
		return nil, errors.New("decoding the look-closely mark: it has no reasons")
	}
	known := MarkReasons()
	for _, c := range mark.Reasons {
		if !slices.Contains(known, c.Reason) || c.Files < 1 {
			return nil, fmt.Errorf("decoding the look-closely mark: %d files for reason %q", c.Files, c.Reason)
		}
	}
	return &mark, nil
}

// lookCloselyOf works out the mark of diff, a change of mr, with the
// scripts of mr's target's jobs.
func (s *service) lookCloselyOf(ctx context.Context, mr *MergeRequest, diff *agent.RepoDiff) LookClosely {
	return LookCloselyAt(diff.Files, s.targetJobScripts(ctx, mr))
}

// targetJobScripts reads the scripts of the jobs mr's pipeline runs: those
// of its target branch's pipeline, never its change's. Failing to read
// them only leaves the files they name unmarked, so it is logged.
func (s *service) targetJobScripts(ctx context.Context, mr *MergeRequest) []string {
	s.mu.Lock()
	reports := s.reports
	s.mu.Unlock()
	if reports == nil {
		return nil
	}
	scripts, err := reports.JobScripts(ctx, mr.UserID, mr.ProjectID, mr.TargetBranch)
	if err != nil && ctx.Err() == nil {
		log.Printf("merge request %d: reading %s's jobs for the look-closely mark: %v", mr.ID, mr.TargetBranch, err)
	}
	return scripts
}

// recordLookClosely works out mr's look-closely mark from diff, the change
// of one of its heads, and keeps its summary for that head, unless the
// merge request has moved past it or keeps the same. Failing to keep it
// costs only the list's mark, so it is logged.
func (s *service) recordLookClosely(ctx context.Context, mr *MergeRequest, diff *agent.RepoDiff) LookClosely {
	mark := s.lookCloselyOf(ctx, mr, diff)
	if diff.HeadSHA == "" || diff.HeadSHA != mr.HeadSHA {
		return mark
	}
	if kept := mr.LookClosely; kept != nil && slices.Equal(kept.Reasons, mark.Reasons) {
		return mark
	}
	if err := s.repo.SetLookClosely(ctx, mr.ID, diff.HeadSHA, mark.Summary()); err != nil {
		log.Printf("merge request %d: keeping its look-closely mark: %v", mr.ID, err)
	}
	return mark
}

// markChange works out and keeps the look-closely mark of open merge
// request mrID's head, unless it has it already.
func (s *service) markChange(ctx context.Context, mrID int64) {
	mr, err := s.repo.GetByID(ctx, mrID)
	if err != nil || mr == nil || mr.Status != StatusOpen || mr.LookClosely != nil || mr.HeadSHA == "" {
		return
	}
	diff, err := s.projects.Diff(ctx, mr.UserID, mr.ProjectID, mr.TargetBranch, mr.HeadSHA)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("merge request %d: reading its change for the look-closely mark: %v", mr.ID, err)
		}
		return
	}
	s.recordLookClosely(ctx, mr, diff)
}

// markOpen works out, as devpad starts, the look-closely mark of each open
// merge request whose head has none yet, such as one that moved while
// devpad was stopping, or whose kept mark didn't read.
func (s *service) markOpen(ctx context.Context) {
	open, err := s.repo.ListOpen(ctx)
	if err != nil {
		log.Printf("reading the open merge requests to mark: %v", err)
		return
	}
	for _, mr := range open {
		if ctx.Err() != nil {
			return
		}
		if mr.LookClosely == nil {
			s.markChange(ctx, mr.ID)
		}
	}
}
