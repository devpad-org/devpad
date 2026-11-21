package task

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/devpad-org/devpad/internal/project"
)

// A blocking link holds a task to do back: Start leaves it to start when
// unblocked instead of queueing it, and it is queued once every task it is
// blocked by is merged, or its last link to an unmerged one is removed. A
// task is blocked only while it is to do, so a task that has started never
// waits again, and the tasks it waited for are all merged by then. A
// cancelled task blocks for good, until the user removes the link, so
// cancelling a task never lets the work that waited for it go ahead by
// mistake. A task waits only for work that lands on the branch it starts
// from, so it is blocked only by tasks that merge into that branch.

func (s *service) Links(ctx context.Context, userID, projectID int64) (map[int64]Links, error) {
	if _, err := s.projects.Get(ctx, userID, projectID); err != nil {
		return nil, err
	}
	links, err := s.repo.ListLinks(ctx, projectID)
	if err != nil {
		return nil, err
	}
	byTask := map[int64][]Link{}
	for _, l := range links {
		byTask[l.From] = append(byTask[l.From], l)
		byTask[l.To] = append(byTask[l.To], l)
	}
	out := make(map[int64]Links, len(byTask))
	for id, ls := range byTask {
		out[id] = linksOf(id, ls)
	}
	return out, nil
}

func (s *service) Link(ctx context.Context, userID, projectID, taskID int64, kind LinkKind, otherID int64) (*Task, error) {
	if taskID == otherID {
		return nil, invalid("a task cannot be linked to itself")
	}
	var link Link
	switch kind {
	case LinkBlockedBy:
		link = Link{From: otherID, To: taskID, Blocks: true}
	case LinkBlocks:
		link = Link{From: taskID, To: otherID, Blocks: true}
	case LinkRelated:
		link = Link{From: min(taskID, otherID), To: max(taskID, otherID)}
	default:
		return nil, invalid("unknown link kind %q", kind)
	}
	unlock := s.lockPair(taskID, otherID)
	defer unlock()
	t, err := s.get(ctx, userID, projectID, taskID)
	if err != nil {
		return nil, err
	}
	other, err := s.linkTarget(ctx, userID, projectID, otherID)
	if err != nil {
		return nil, err
	}
	if link.Blocks {
		p, err := s.projects.Get(ctx, userID, projectID)
		if err != nil {
			return nil, err
		}
		blocker, blocked := t, other
		if link.From == other.ID {
			blocker, blocked = other, t
		}
		if blocked.Status != StatusTodo {
			return nil, fmt.Errorf("%w: task #%d has already started, so it can no longer wait for another task", ErrInvalidState, blocked.ID)
		}
		if blocker.Status == StatusCancelled {
			return nil, cancelledBlocker(blocker)
		}
		if err := buildsOn(p, blocker, fmt.Sprintf("task #%d", blocked.ID), blocked.BaseBranch); err != nil {
			return nil, err
		}
	}
	unlockLinks := s.lockLinks(projectID)
	defer unlockLinks()
	if link.Blocks {
		links, err := s.repo.ListLinks(ctx, projectID)
		if err != nil {
			return nil, err
		}
		if waitsFor(links, link.From, link.To) {
			return nil, invalid("task #%d already waits for task #%d, so it cannot block it as well", link.From, link.To)
		}
	}
	if err := s.repo.CreateLink(ctx, projectID, link); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *service) Unlink(ctx context.Context, userID, projectID, taskID, otherID int64) (*Task, error) {
	unlock := s.lockPair(taskID, otherID)
	if _, err := s.get(ctx, userID, projectID, taskID); err != nil {
		unlock()
		return nil, err
	}
	if _, err := s.linkTarget(ctx, userID, projectID, otherID); err != nil {
		unlock()
		return nil, err
	}
	found, err := s.repo.DeleteLink(ctx, taskID, otherID)
	unlock()
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, invalid("tasks #%d and #%d are not linked", taskID, otherID)
	}
	// Either task may have been waiting for the other. The link is gone, so
	// a start that fails is recorded on its task rather than undoing it.
	ctx = context.WithoutCancel(ctx)
	s.startIfUnblocked(ctx, taskID)
	s.startIfUnblocked(ctx, otherID)
	return s.get(ctx, userID, projectID, taskID)
}

func (s *service) CancelStart(ctx context.Context, userID, projectID, taskID int64) (*Task, error) {
	unlock := s.lock(taskID)
	defer unlock()
	t, err := s.get(ctx, userID, projectID, taskID)
	if err != nil {
		return nil, err
	}
	if !t.StartWhenUnblocked {
		return t, nil
	}
	t.StartWhenUnblocked = false
	return t, s.repo.Update(ctx, t)
}

// pendingLink is a link to make from a task being created: other blocks
// it, or is related to it.
type pendingLink struct {
	other  *Task
	blocks bool
}

// newTaskLinks checks the links a new task is created with, before it is
// stored, so a link that cannot be made leaves no task behind.
func (s *service) newTaskLinks(ctx context.Context, p *project.Project, userID int64, in CreateInput) ([]pendingLink, error) {
	seen := map[int64]bool{}
	var out []pendingLink
	add := func(id int64, blocks bool) error {
		if seen[id] {
			return invalid("task #%d is linked more than once", id)
		}
		seen[id] = true
		other, err := s.linkTarget(ctx, userID, p.ID, id)
		if err != nil {
			return err
		}
		if blocks && other.Status == StatusCancelled {
			return cancelledBlocker(other)
		}
		if blocks {
			if err := buildsOn(p, other, "the new task", in.BaseBranch); err != nil {
				return err
			}
		}
		out = append(out, pendingLink{other: other, blocks: blocks})
		return nil
	}
	for _, id := range in.BlockedBy {
		if err := add(id, true); err != nil {
			return nil, err
		}
	}
	for _, id := range in.Related {
		if err := add(id, false); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// linkNew stores a new task's links. The task was listed from the moment it
// was stored, so a link made from another task since may already have it
// block something: each blocking link is checked for a cycle like any other.
func (s *service) linkNew(ctx context.Context, t *Task, links []pendingLink) error {
	if len(links) == 0 {
		return nil
	}
	unlock := s.lock(t.ID)
	defer unlock()
	unlockLinks := s.lockLinks(t.ProjectID)
	defer unlockLinks()
	existing, err := s.repo.ListLinks(ctx, t.ProjectID)
	if err != nil {
		return err
	}
	for _, l := range links {
		link := Link{From: l.other.ID, To: t.ID, Blocks: true}
		if !l.blocks {
			link = Link{From: min(t.ID, l.other.ID), To: max(t.ID, l.other.ID)}
		}
		if link.Blocks && waitsFor(existing, link.From, link.To) {
			return invalid("task #%d already waits for the new task, so it cannot block it as well", link.From)
		}
		if err := s.repo.CreateLink(ctx, t.ProjectID, link); err != nil {
			return err
		}
		existing = append(existing, link)
	}
	return nil
}

// linkTarget loads the other task of a link, which must be in the same
// project.
func (s *service) linkTarget(ctx context.Context, userID, projectID, id int64) (*Task, error) {
	t, err := s.get(ctx, userID, projectID, id)
	if errors.Is(err, ErrNotFound) {
		return nil, invalid("task #%d is not in this project", id)
	}
	return t, err
}

func cancelledBlocker(t *Task) error {
	return fmt.Errorf("%w: task #%d is cancelled, so it will never be merged", ErrInvalidState, t.ID)
}

// buildsOn refuses to make a task blocked by one whose work would not be
// on the branch it starts from, base as a task stores it or as requested
// for a new one. who names the blocked task in the error.
func buildsOn(p *project.Project, blocker *Task, who, base string) error {
	from, into := startsFrom(p, base), mergesInto(p, blocker)
	if from == into {
		return nil
	}
	return invalid("task #%d merges into %s, but %s starts from %s, so it would start without that work", blocker.ID, into, who, from)
}

// startsFrom is the branch a task's own branch is made from: its base, or
// the project's default branch when it has none.
func startsFrom(p *project.Project, base string) string {
	if base = strings.TrimSpace(base); base != "" {
		return base
	}
	return p.DefaultBranch
}

// mergesInto is the branch a task's merge request merges into. That is
// always the project's default branch for now; were a task's merge request
// to target its base instead, this is what would change.
func mergesInto(p *project.Project, _ *Task) string {
	return p.DefaultBranch
}

// waitsFor reports whether task waiter is blocked by task blocker, directly
// or through other tasks.
func waitsFor(links []Link, waiter, blocker int64) bool {
	next := map[int64][]int64{}
	for _, l := range links {
		if l.Blocks {
			next[l.From] = append(next[l.From], l.To)
		}
	}
	seen := map[int64]bool{blocker: true}
	queue := []int64{blocker}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, to := range next[id] {
			if to == waiter {
				return true
			}
			if !seen[to] {
				seen[to] = true
				queue = append(queue, to)
			}
		}
	}
	return false
}

// blocked reports whether a task is blocked by a task not merged yet.
func (s *service) blocked(ctx context.Context, taskID int64) (bool, error) {
	links, err := s.repo.LinksOf(ctx, taskID)
	if err != nil {
		return false, err
	}
	return linksOf(taskID, links).Blocked, nil
}

// unblock starts, in the background, the project's tasks left to start
// when unblocked that nothing holds back any more.
func (s *service) unblock(projectID int64) {
	s.spawn(func(ctx context.Context) {
		tasks, err := s.repo.ListByProjectID(ctx, projectID)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("project %d: listing tasks to unblock: %v", projectID, err)
			}
			return
		}
		// The list is read without the tasks' locks, so a Start that saw a
		// blocker not merged yet may still be about to leave its task to
		// start when unblocked. Every task to do is checked again under its
		// lock, which waits for that Start, rather than skipped by a flag
		// read too early.
		for _, t := range tasks {
			if t.Status == StatusTodo {
				s.startIfUnblocked(ctx, t.ID)
			}
		}
	})
}

// startIfUnblocked queues a task left to start when unblocked once nothing
// holds it back. A start that fails is recorded on the task, which then
// waits for the user to start it.
func (s *service) startIfUnblocked(ctx context.Context, taskID int64) {
	unlock := s.lock(taskID)
	defer unlock()
	t, err := s.repo.GetByID(ctx, taskID)
	if err != nil {
		log.Printf("task %d: loading it to unblock: %v", taskID, err)
		return
	}
	if t == nil || t.Status != StatusTodo || !t.StartWhenUnblocked {
		return
	}
	blocked, err := s.blocked(ctx, taskID)
	if err != nil {
		log.Printf("task %d: reading its links: %v", taskID, err)
		return
	}
	if blocked {
		return
	}
	if _, startErr := s.start(ctx, t, StartInput{}); startErr != nil {
		if ctx.Err() != nil {
			return
		}
		// start may have changed t before failing, so the error goes on the
		// task as stored.
		t, err := s.repo.GetByID(ctx, taskID)
		if err != nil || t == nil {
			log.Printf("task %d: could not start once unblocked: %v", taskID, startErr)
			return
		}
		t.StartWhenUnblocked = false
		t.Error = fmt.Sprintf("nothing blocks it now, but its agent could not start: %v", startErr)
		if err := s.repo.Update(ctx, t); err != nil {
			log.Printf("task %d: recording that it could not start once unblocked: %v", taskID, err)
		}
	}
}

// linkedTask is what a task's agent is told about a linked task.
type linkedTask struct {
	id     int64
	title  string
	status Status
}

// linkedTasks are a task's links, as its agent is told about them.
type linkedTasks struct {
	blockedBy, blocks, related []linkedTask
}

// linkedTasks loads what the task's agent is told about its links. They
// are only background, so failing to read them leaves them out.
func (s *service) linkedTasks(ctx context.Context, t *Task) linkedTasks {
	links, err := s.repo.LinksOf(ctx, t.ID)
	if err != nil {
		log.Printf("task %d: reading its links for the prompt: %v", t.ID, err)
		return linkedTasks{}
	}
	l := linksOf(t.ID, links)
	load := func(ids []int64) []linkedTask {
		var out []linkedTask
		for _, id := range ids {
			other, err := s.repo.GetByID(ctx, id)
			if err != nil || other == nil {
				continue
			}
			out = append(out, linkedTask{id: other.ID, title: other.Title, status: other.Status})
		}
		return out
	}
	return linkedTasks{blockedBy: load(l.BlockedBy), blocks: load(l.Blocks), related: load(l.Related)}
}

func (l linkedTasks) prompt() string {
	if len(l.blockedBy)+len(l.blocks)+len(l.related) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Linked tasks\n\n")
	section := func(intro string, tasks []linkedTask) {
		if len(tasks) == 0 {
			return
		}
		b.WriteString(intro + "\n\n")
		for _, t := range tasks {
			title := strings.Join(strings.Fields(t.title), " ")
			fmt.Fprintf(&b, "- #%d %s (%s)\n", t.id, title, strings.ReplaceAll(string(t.status), "_", " "))
		}
		b.WriteString("\n")
	}
	section("This task waited for these tasks to be merged into the project. Their work is done: build on it rather than redo it.", l.blockedBy)
	section("These tasks wait for this one to be merged, so leave your work in a state they can build on.", l.blocks)
	section("These tasks are related to this one. Each is worked on separately, so don't do their work here.", l.related)
	return b.String()
}
