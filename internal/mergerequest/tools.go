package mergerequest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aitools "github.com/devpad-org/devpad/internal/ai/tools"
)

// Review agents record their review with these tools, which are offered
// only to runs in a review sandbox whose review is still pending. They run on
// the host, so an agent cannot reach the review records any other way.
const (
	commentTool = "comment_on_line"
	submitTool  = "submit_review"
)

var reviewTools = []domain.ToolDefinition{
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        commentTool,
			Description: "Comment on one line of a file changed by the merge request under review. Use it for specific problems; put general feedback in submit_review.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path of a changed file, relative to the repo root."},"line":{"type":"integer","minimum":1,"description":"Line number in the new version of the file."},"body":{"type":"string","description":"The comment, in Markdown."}},"required":["path","line","body"]}`),
		},
	},
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        submitTool,
			Description: "Submit your review of the merge request. Call it once, after your line comments; the review is final.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"verdict":{"type":"string","enum":["approve","request_changes","comment"],"description":"approve: it can be merged as it is. request_changes: something must change first. comment: feedback that should not block it."},"body":{"type":"string","description":"A summary of the review that stands on its own, in Markdown."}},"required":["verdict","body"]}`),
		},
	},
}

func (s *service) Tools(ctx context.Context, userID, workspaceID int64) []domain.ToolDefinition {
	if _, _, err := s.pendingReview(ctx, userID, workspaceID); err != nil {
		return nil
	}
	return reviewTools
}

// pendingReview finds the review a workspace's agent is writing.
func (s *service) pendingReview(ctx context.Context, userID, workspaceID int64) (*Review, *MergeRequest, error) {
	rv, err := s.repo.GetPendingReviewByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if rv == nil {
		return nil, nil, ErrNotFound
	}
	mr, err := s.repo.GetByID(ctx, rv.MergeRequestID)
	if err != nil {
		return nil, nil, err
	}
	if mr == nil || mr.UserID != userID {
		return nil, nil, ErrNotFound
	}
	return rv, mr, nil
}

func (s *service) ExecuteTool(ctx context.Context, req aitools.ExecutionRequest) (domain.ToolResultPart, bool) {
	if req.ToolName != commentTool && req.ToolName != submitTool {
		return domain.ToolResultPart{}, false
	}
	found, _, err := s.pendingReview(ctx, req.UserID, req.WorkspaceID)
	if err != nil {
		return toolError(req.ToolName, "there is no pending review in this workspace"), true
	}
	unlock := s.lock("review", found.ID)
	defer unlock()
	rv, mr, err := s.pendingReview(ctx, req.UserID, req.WorkspaceID)
	if err != nil || rv.ID != found.ID {
		return toolError(req.ToolName, "the review has already been submitted"), true
	}
	if mr.Status != StatusOpen {
		return toolError(req.ToolName, "the merge request is %s", mr.Status), true
	}
	if req.ToolName == commentTool {
		return s.commentOnLine(ctx, mr, rv, req.Arguments), true
	}
	return s.submitReview(ctx, mr, rv, req.Arguments), true
}

func (s *service) commentOnLine(ctx context.Context, mr *MergeRequest, rv *Review, raw json.RawMessage) domain.ToolResultPart {
	var args struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return toolError(commentTool, "invalid arguments: %v", err)
	}
	args.Path = strings.TrimPrefix(args.Path, "./")
	if err := validateComment(args.Path, args.Line, args.Body); err != nil {
		return toolError(commentTool, "%v", err)
	}
	diff, err := s.projects.Diff(ctx, mr.UserID, mr.ProjectID, mr.TargetBranch, rv.HeadSHA)
	if err != nil {
		return toolError(commentTool, "reading the change: %v", err)
	}
	var changed []string
	for _, f := range diff.Files {
		changed = append(changed, f.Path)
	}
	if !slices.Contains(changed, args.Path) {
		return toolError(commentTool, "%s is not changed by this merge request. Changed files: %s", args.Path, strings.Join(changed, ", "))
	}
	n, err := s.repo.CountReviewComments(ctx, rv.ID)
	if err != nil {
		return toolError(commentTool, "%v", err)
	}
	if n >= maxCommentsPerReview {
		return toolError(commentTool, "this review already has %d comments, the most it can have; put anything else in submit_review", n)
	}
	c := &Comment{MergeRequestID: mr.ID, ReviewID: rv.ID, Author: AuthorAgent, Path: args.Path, Line: args.Line, Body: strings.TrimSpace(args.Body), HeadSHA: rv.HeadSHA}
	if err := s.repo.CreateComment(ctx, c); err != nil {
		return toolError(commentTool, "%v", err)
	}
	return domain.ToolResultPart{Name: commentTool, Content: fmt.Sprintf("Comment added on %s:%d.", c.Path, c.Line)}
}

func (s *service) submitReview(ctx context.Context, mr *MergeRequest, rv *Review, raw json.RawMessage) domain.ToolResultPart {
	var args struct {
		Verdict Verdict `json:"verdict"`
		Body    string  `json:"body"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return toolError(submitTool, "invalid arguments: %v", err)
	}
	if !args.Verdict.Valid() {
		return toolError(submitTool, "verdict must be approve, request_changes or comment")
	}
	body := strings.TrimSpace(args.Body)
	if body == "" {
		return toolError(submitTool, "body is required: summarize your review")
	}
	if len(body) > maxReviewBodyBytes {
		return toolError(submitTool, "the summary is too long")
	}
	rv.Status, rv.Verdict, rv.Body = ReviewSubmitted, args.Verdict, body
	if err := s.repo.UpdateReview(ctx, rv); err != nil {
		return toolError(submitTool, "%v", err)
	}

	if rv.Verdict == VerdictRequestChanges && mr.TaskID != 0 {
		comments, err := s.repo.ListComments(ctx, mr.ID)
		if err != nil {
			log.Printf("review %d: listing comments: %v", rv.ID, err)
		}
		var own []*Comment
		for _, c := range comments {
			if c.ReviewID == rv.ID {
				own = append(own, c)
			}
		}
		if _, err := s.tasks.RequestChanges(ctx, mr.UserID, mr.ProjectID, mr.TaskID, feedback(rv, own), rv.AutoFix); err != nil {
			log.Printf("review %d: requesting changes on task %d: %v", rv.ID, mr.TaskID, err)
		}
	}
	return domain.ToolResultPart{Name: submitTool, Content: "Review submitted. Your work is done; end your turn with a one-line summary."}
}

func toolError(name, format string, args ...any) domain.ToolResultPart {
	return domain.ToolResultPart{Name: name, Content: fmt.Sprintf(format, args...), IsError: true}
}
