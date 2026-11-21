package task

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"unicode"
	"unicode/utf8"

	aitools "github.com/devpad-org/devpad/internal/ai/tools"
	"github.com/devpad-org/devpad/internal/workspace"
)

// Comments are a task's comments, oldest first.
func (s *service) Comments(ctx context.Context, userID, projectID, taskID int64) ([]*Comment, error) {
	if _, err := s.get(ctx, userID, projectID, taskID); err != nil {
		return nil, err
	}
	return s.repo.ListComments(ctx, taskID)
}

// AddComment leaves author's comment on the task. Each agent author can
// leave at most maxAgentComments on one task.
func (s *service) AddComment(ctx context.Context, userID, projectID, taskID int64, author Author, body string) (*Comment, error) {
	body, err := commentBody(author, body)
	if err != nil {
		return nil, err
	}
	if _, err := s.get(ctx, userID, projectID, taskID); err != nil {
		return nil, err
	}
	limit := maxAgentComments
	if author == AuthorUser {
		limit = 0
	}
	c := &Comment{TaskID: taskID, Author: author, Body: body}
	if err := s.repo.CreateComment(ctx, c, limit); err != nil {
		if errors.Is(err, ErrCommentLimit) {
			return nil, fmt.Errorf("%w: you have left %d comments on this task, the most you can", ErrCommentLimit, limit)
		}
		return nil, err
	}
	return c, nil
}

// EditComment replaces the body of a comment author wrote on the task. The
// earlier versions are kept, and the user can read every one of them.
func (s *service) EditComment(ctx context.Context, userID, projectID, taskID, commentID int64, author Author, body string) (*Comment, error) {
	body, err := commentBody(author, body)
	if err != nil {
		return nil, err
	}
	if _, err := s.comment(ctx, userID, projectID, taskID, commentID); err != nil {
		return nil, err
	}
	c, err := s.repo.EditComment(ctx, commentID, author, body)
	if errors.Is(err, ErrCommentLimit) {
		return nil, fmt.Errorf("%w: the comment has been edited the most times it can be; leave a new comment instead", ErrCommentLimit)
	}
	return c, err
}

// CommentRevisions are every version of a comment on the task, oldest first.
func (s *service) CommentRevisions(ctx context.Context, userID, projectID, taskID, commentID int64) ([]*CommentRevision, error) {
	if _, err := s.comment(ctx, userID, projectID, taskID, commentID); err != nil {
		return nil, err
	}
	return s.repo.ListCommentRevisions(ctx, commentID)
}

// comment finds a comment on one of the user's tasks.
func (s *service) comment(ctx context.Context, userID, projectID, taskID, commentID int64) (*Comment, error) {
	if _, err := s.get(ctx, userID, projectID, taskID); err != nil {
		return nil, err
	}
	c, err := s.repo.GetComment(ctx, commentID)
	if err != nil {
		return nil, err
	}
	if c == nil || c.TaskID != taskID {
		return nil, ErrCommentNotFound
	}
	return c, nil
}

// commentBody checks a comment's body and trims it. An agent's comment
// must not hold characters a screen does not show, which a model still
// reads: the user is to see all of what an agent says to another.
func commentBody(author Author, body string) (string, error) {
	body = strings.TrimSpace(body)
	switch {
	case body == "":
		return "", invalid("the comment is empty")
	case len(body) > maxCommentBytes:
		return "", invalid("the comment is longer than %d bytes", maxCommentBytes)
	case !utf8.ValidString(body):
		return "", invalid("the comment is not valid UTF-8")
	}
	if author != AuthorUser {
		if r, ok := invisibleIn(body); ok {
			return "", invalid("the comment holds an invisible character, U+%04X, where a screen does not show it; write only what the user can see", r)
		}
		// The browser decodes HTML character references, such as
		// &#x200B; or &ZeroWidthSpace;, into the characters they name.
		if r, ok := invisibleIn(html.UnescapeString(body)); ok {
			return "", invalid("the comment holds an HTML character reference to an invisible character, U+%04X, where a screen does not show it; write only what the user can see", r)
		}
	}
	return body, nil
}

// invisibleIn returns the first character in s that a screen does not show
// where it stands.
func invisibleIn(s string) (rune, bool) {
	prev := rune(-1)
	for _, r := range s {
		if invisible(r, prev) {
			return r, true
		}
		prev = r
	}
	return 0, false
}

// invisible reports whether r, after prev (-1 at the start), takes no
// visible space: a control or format character (bidi controls, zero-width
// spaces, Unicode tags), another default-ignorable one (such as a Hangul
// filler), or a variation selector, any of which can carry hidden bytes
// after a visible character. Emoji keep the few they need, one at a time:
// U+FE0E or U+FE0F, which pick text or emoji presentation, right after a
// visible character, and the zero-width joiner after one or after such a
// selector.
func invisible(r, prev rune) bool {
	switch r {
	case '\n', '\r', '\t':
		return false
	case '\uFE0E', '\uFE0F':
		return !visibleBase(prev)
	case '\u200D':
		return !visibleBase(prev) && prev != '\uFE0E' && prev != '\uFE0F'
	}
	return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Other_Default_Ignorable_Code_Point, unicode.Variation_Selector)
}

// visibleBase reports whether a presentation selector or joiner may follow
// r: r is a character that shows, not the start, a space or one of them.
func visibleBase(r rune) bool {
	return r >= 0 && !unicode.IsSpace(r) && r != '\u200D' && r != '\uFE0E' && r != '\uFE0F'
}

// SandboxTask is the task whose sandbox ws is, for a run's scope, or nil
// when ws is no open task's sandbox.
func (s *service) SandboxTask(ctx context.Context, userID int64, ws *workspace.Workspace) (*aitools.TaskScope, error) {
	if ws.Kind != workspace.KindTask || ws.ProjectID == 0 {
		return nil, nil
	}
	t, err := s.repo.GetByWorkspaceID(ctx, ws.ID)
	if err != nil {
		return nil, err
	}
	if t == nil || t.UserID != userID || t.ProjectID != ws.ProjectID || t.Branch != ws.Branch || t.Status.Closed() {
		return nil, nil
	}
	return &aitools.TaskScope{TaskID: t.ID}, nil
}
