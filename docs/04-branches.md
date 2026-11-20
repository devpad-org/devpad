---
title: Branches and merge requests
summary: Publishing branches, protected branches, merge requests, reviews by you and by agents, and merging.
---

Every change reaches a project's protected branches through a merge request. Branches move between your workspace and the project only when you publish or pull them, and devpad checks each move.

## The project's branches {#project-branches}

The project's branches are those in its canonical repo: the ones you've published, the ones agents work on (`task/<id>`), and the protected branches. In your workspace, `git fetch devpad` shows them as `devpad/<branch>`. The branch picker in **Source Control** groups branches as **Local**, **Project** and **Remote**, and tags each one the project or an agent has.

## Protected branches {#protected-branches}

The default branch is always protected, and you can protect others with **Manage branches** on the Project page, such as `main` in a project that works on `develop`. A protected branch:

- changes only through a merge request: your workspace can't publish to it, and no agent can own it;
- is what merge requests target, so features can merge into `develop` and releases from `develop` into `main`;
- is the only kind of branch devpad pushes [upstream](/docs/projects#upstream), and the only kind a sync fast-forwards;
- can't be deleted.

The **Branches** dialog also sets the **Default branch**, which tasks and your workspace start from and merge requests target unless you pick another. Changing it keeps the old default protected. Protection changes take effect when you press **Save**. A project can protect up to 20 branches besides its default.

If you're on a protected branch in your workspace, Source Control says so and offers **New branch**: start a branch, publish it, and open a merge request.

## Publishing {#publishing}

Publishing sends a branch from your workspace to the project, where merge requests and agents can see it. Press **Publish** in the Source Control panel, or run `git push devpad <branch>` (a plain `git push` does the same once the branch tracks `devpad/<branch>`, which it does after its first push). The IDE shows a notice for every push, whether you, a script or the assistant made it.

Devpad refuses to publish:

- **a protected branch**: merge into it through a merge request;
- **a branch an agent's sandbox owns**: you can pull and follow an agent's branch, but not publish to it; check it out under a new name to take it over;
- **anything but a fast-forward**: if the project's copy has commits yours lacks, merge `devpad/<branch>` first. Force-pushes are refused;
- **a branch already being published**: try again in a moment.

When a branch with an open merge request is published, the merge request moves to the new commit. Source Control's card also says when the project has commits you don't (**Pull**), and **Update from &lt;base&gt;** merges the latest of the branch you're merging into.

`git push origin` is refused: `origin` is a read-only view of the upstream. `git push devpad :<branch>` is refused too: branches are deleted in the Branches dialog.

## Deleting branches {#deleting-branches}

Only you delete branches, from the **Branches** dialog: **Delete** beside a branch deletes it at once, without Save. A branch can't be deleted while it's:

- protected, or the default branch;
- owned by a sandbox (**used by a sandbox**);
- the source of an open merge request (**used by merge request !N**): merge or close it first;
- worked on, or started from, by a task that isn't merged or cancelled (**used by task #N**): cancel or delete the task first.

Devpad also refuses if the branch has moved since the dialog showed it, so you never delete commits you haven't seen.

## Merge requests {#merge-requests}

A merge request asks to merge one branch into a protected branch. Open one:

- from **Source Control**, with **Merge request** under the branch's card (**Publish and open** publishes it first);
- from the **Merge requests** list, with **New merge request**: pick the branch to merge and the protected branch to merge it **Into**;
- by asking the [assistant](/docs/tasks#the-assistant), which names the branch to merge into and waits for your approval; the merge request's activity says the assistant opened it;
- automatically, when a [task's agent](/docs/tasks#how-a-task-runs) finishes with new commits: devpad opens a merge request from the task's branch into the default branch.

A branch has at most one open merge request. The source can itself be protected, as when releasing `develop` into `main`.

The merge request's **Overview** has its description, the merge card, and its **Activity**: reviews, comment threads, and each push to the branch. **Changes** shows the diff file by file; click a line number to comment on that line.

## Threads {#threads}

Each comment starts a thread that can be replied to and resolved. Resolved threads fold away. A thread marked **blocking** must be addressed before the change can merge: comments in a review that requests changes are blocking. The review agent resolves the threads a later commit addresses, and you can resolve or reopen any thread.

## Reviews {#reviews}

**Review** on a merge request opens your review: a summary, the line comments you've added to it, and a verdict, **Comment**, **Approve** or **Request changes**. On a task's merge request, requesting changes records your feedback on the task, and **Send the feedback to the task's agent now** sends it straight away; otherwise use **Send back to agent** later.

A **review agent** reads the change in a sandbox of its own, leaves comments and a verdict, and resolves the threads the change addresses. Start one under **Agent review** in the sidebar, or let the project start one on each new merge request (**Review new merge requests automatically**, on by default). **Re-review** runs the last agent review again on the latest commit. If devpad no longer offers that review's model, the new review uses your default model for the agent, or else the task's.

The review agent takes its instructions, `AGENTS.md` and [skills](/docs/tasks#skills), from the branch the change would merge into, at the commit it compares against, never from the change itself: a change can't tell its reviewer how to review it.

## Auto-fixing reviews {#auto-fixing-reviews}

On a task's merge request, a review can send the changes it requests to the task's agent and review the commits it answers with: tick **Send requested changes to the task's agent, and review its fixes**, as automatic reviews of task merge requests do. That goes on for up to three reviews. The third review's requested changes are recorded on the task but wait for you, and a review of yours ends the loop.

A review spends from its own budget and, on a task's merge request, from the task's too. If a review stops because a budget ran out, **Raise and continue the review** resumes it where it stopped, in the same sandbox, on the same model unless devpad no longer offers it. See [Budgets](/docs/ai#budgets).

## Merging {#merging}

The merge card lists where the merge request stands: approvals, open threads, reviews or agents still at work, and conflicts. These are for you to weigh: merging isn't blocked by them, and only you can merge, since agents and the assistant have no way to. Pick a **Merge strategy**:

- **Merge commit** keeps every commit and records a merge commit.
- **Squash** combines the branch into a single commit. It isn't offered when the source is a protected branch.
- **Fast-forward** moves the target forward with no new commit, if the target hasn't moved on.

With an upstream, **Push &lt;target&gt; upstream** (on by default) pushes the target after the merge. Only the commit that was reviewed merges: if the branch has moved since, review the new commits first.

If the branch conflicts with its target, the card lists the conflicting files; on a task's merge request, **Ask the task's agent to resolve them** has the agent merge the target in. Merging a task's merge request marks the task **Merged**.

**Close merge request** closes it without merging. It can't be reopened, and the branch stays.
