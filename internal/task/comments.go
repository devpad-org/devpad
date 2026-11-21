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
			return "", invalid("the comment holds an invisible character, U+%04X; write only what the user can see", r)
		}
		// The browser decodes HTML character references, such as
		// &#x200B; or &ZeroWidthSpace;, into the characters they name.
		if r, ok := invisibleIn(html.UnescapeString(body)); ok {
			return "", invalid("the comment holds an HTML character reference to an invisible character, U+%04X; write only what the user can see", r)
		}
	}
	return body, nil
}

// invisibleIn returns the first invisible character in s.
func invisibleIn(s string) (rune, bool) {
	i := strings.IndexFunc(s, invisible)
	if i < 0 {
		return 0, false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r, true
}

// invisible reports whether r takes no visible space in text: a control or
// format character (bidi controls, zero-width spaces, Unicode tags), or a
// variation selector that does not pick an emoji's or text's presentation,
// which can carry hidden bytes after a visible character. The zero-width
// joiner stays, since emoji sequences need it.
func invisible(r rune) bool {
	switch {
	case r == '\n' || r == '\r' || r == '\t' || r == '\u200d':
		return false
	case unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r):
		return true
	case r >= 0xFE00 && r <= 0xFE0D, r >= 0xE0100 && r <= 0xE01EF:
		return true
	}
	return false
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
