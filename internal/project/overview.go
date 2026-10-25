package project

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Overview summarizes what is happening in a project, for the launcher.
type Overview struct {
	ProjectID int64
	// RunningAgents counts the project's task and review sandboxes that are
	// running.
	RunningAgents int
	// ActiveTasks counts tasks queued for or being worked on by an agent.
	ActiveTasks int
	// OpenMergeRequests counts every open merge request.
	OpenMergeRequests int
	// AwaitingReview counts open merge requests that wait on the user: no
	// agent is reviewing or reworking them, and the user has not reviewed
	// their current head.
	AwaitingReview int
	// LastActivityAt is the latest change to the project, its tasks or its
	// merge requests.
	LastActivityAt time.Time
}

// WorkspaceLink ties a project-linked workspace to what it is for, so an
// agent waiting in it can be traced back to its task or merge request.
type WorkspaceLink struct {
	WorkspaceID    int64
	ProjectID      int64
	Kind           string
	TaskID         int64
	MergeRequestID int64
}

// Overviews returns one overview per project the user owns, plus a link for
// each of their project-linked workspaces.
func (s *service) Overviews(ctx context.Context, userID int64) ([]*Overview, []*WorkspaceLink, error) {
	overviews, err := s.repo.Overviews(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("summarizing projects: %w", err)
	}
	links, err := s.repo.WorkspaceLinks(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("linking project workspaces: %w", err)
	}
	return overviews, links, nil
}

func (r *repository) Overviews(ctx context.Context, userID int64) ([]*Overview, error) {
	byID := map[int64]*Overview{}
	var order []*Overview
	rows, err := r.db.QueryContext(ctx, `SELECT id, updated_at FROM projects WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	err = eachRow(rows, func() error {
		o := &Overview{}
		if err := rows.Scan(&o.ProjectID, &o.LastActivityAt); err != nil {
			return err
		}
		byID[o.ProjectID] = o
		order = append(order, o)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning projects: %w", err)
	}

	counts := []struct {
		query string
		field func(*Overview) *int
	}{
		{
			`SELECT project_id, COUNT(*) FROM workspaces
			 WHERE user_id = ? AND project_id IS NOT NULL AND kind != 'interactive' AND status = 'running'
			 GROUP BY project_id`,
			func(o *Overview) *int { return &o.RunningAgents },
		},
		{
			`SELECT project_id, COUNT(*) FROM tasks
			 WHERE user_id = ? AND status IN ('queued','in_progress')
			 GROUP BY project_id`,
			func(o *Overview) *int { return &o.ActiveTasks },
		},
		{
			`SELECT project_id, COUNT(*) FROM merge_requests
			 WHERE user_id = ? AND status = 'open'
			 GROUP BY project_id`,
			func(o *Overview) *int { return &o.OpenMergeRequests },
		},
		{
			`SELECT mr.project_id, COUNT(*) FROM merge_requests mr
			 WHERE mr.user_id = ? AND mr.status = 'open'
			   AND NOT EXISTS (SELECT 1 FROM merge_request_reviews rv
			                   WHERE rv.merge_request_id = mr.id AND rv.status = 'pending')
			   AND NOT EXISTS (SELECT 1 FROM merge_request_reviews rv
			                   WHERE rv.merge_request_id = mr.id AND rv.author = 'user'
			                     AND rv.status = 'submitted' AND rv.head_sha = mr.head_sha)
			   AND NOT EXISTS (SELECT 1 FROM tasks t
			                   WHERE t.id = mr.task_id AND t.status IN ('queued','in_progress'))
			 GROUP BY mr.project_id`,
			func(o *Overview) *int { return &o.AwaitingReview },
		},
	}
	for _, c := range counts {
		rows, err := r.db.QueryContext(ctx, c.query, userID)
		if err != nil {
			return nil, fmt.Errorf("counting project activity: %w", err)
		}
		err = eachRow(rows, func() error {
			var projectID int64
			var n int
			if err := rows.Scan(&projectID, &n); err != nil {
				return err
			}
			if o := byID[projectID]; o != nil {
				*c.field(o) = n
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scanning project activity: %w", err)
		}
	}

	// MAX() would drop the column type and with it time parsing, so the
	// latest change is picked here.
	for _, table := range []string{"tasks", "merge_requests"} {
		rows, err := r.db.QueryContext(ctx, `SELECT project_id, updated_at FROM `+table+` WHERE user_id = ?`, userID)
		if err != nil {
			return nil, fmt.Errorf("listing %s activity: %w", table, err)
		}
		err = eachRow(rows, func() error {
			var projectID int64
			var at time.Time
			if err := rows.Scan(&projectID, &at); err != nil {
				return err
			}
			if o := byID[projectID]; o != nil && at.After(o.LastActivityAt) {
				o.LastActivityAt = at
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scanning %s activity: %w", table, err)
		}
	}
	return order, nil
}

func (r *repository) WorkspaceLinks(ctx context.Context, userID int64) ([]*WorkspaceLink, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT w.id, w.project_id, w.kind,
		       COALESCE((SELECT t.id FROM tasks t WHERE t.workspace_id = w.id ORDER BY t.id DESC LIMIT 1), 0),
		       COALESCE((SELECT rv.merge_request_id FROM merge_request_reviews rv WHERE rv.workspace_id = w.id ORDER BY rv.id DESC LIMIT 1), 0)
		FROM workspaces w
		WHERE w.user_id = ? AND w.project_id IS NOT NULL AND w.status != 'deleted'
		ORDER BY w.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("listing project workspaces: %w", err)
	}
	var links []*WorkspaceLink
	err = eachRow(rows, func() error {
		l := &WorkspaceLink{}
		if err := rows.Scan(&l.WorkspaceID, &l.ProjectID, &l.Kind, &l.TaskID, &l.MergeRequestID); err != nil {
			return err
		}
		links = append(links, l)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning project workspaces: %w", err)
	}
	return links, nil
}

// eachRow calls fn for every row, then closes rows and reports any error.
func eachRow(rows *sql.Rows, fn func() error) error {
	defer rows.Close()
	for rows.Next() {
		if err := fn(); err != nil {
			return err
		}
	}
	return rows.Err()
}
