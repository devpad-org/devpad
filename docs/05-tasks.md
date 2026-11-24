---
title: Tasks and agents
summary: Handing work to AI agents with tasks, the assistant in your workspace, approvals, and skills.
---

There are two ways to work with AI in devpad. A **task** hands a change to a background agent, which works on its own branch in a sandbox and comes back with a merge request. The **assistant** works with you, in your own workspace, one message at a time.

## Tasks {#tasks}

The **Tasks** surface lists the project's tasks, with what each has spent and its merge request. Press **New task**:

- **Title** and **Description**: what should change, and how you'll know it works. The agent sees both, with the project's description. Markdown works in the description, but HTML in it shows as the text it is, as it does in the **Feedback for the next run** a review leaves: the assistant can write a task and a review agent its feedback, and neither can hide anything there from you.
- **Start from**: the branch the task's own branch is made from. It must be in the project, so publish it first.
- **Agent**: the model and effort, which start from your [default models](/docs/ai#default-models).
- **Linked tasks**: tasks it's **Blocked by**, whose work it builds on, and tasks it's **Related** to. See [Linked tasks](#linked-tasks).
- **Budget**: what agents may spend on the task across all their runs. Empty uses your default; 0 means no limit.

**Create and start agent** hands it to an agent straight away, or, for a task blocked by tasks not merged yet, **Create, start when unblocked** leaves it to start once they are. **Save as to do** keeps it on the list to start later with **Start agent**.

## Linked tasks {#linked-tasks}

A task's **Linked tasks**, in its sidebar, are the other tasks of the project it's linked to. Press **+** to add one, and the **×** on a row to remove it. Two tasks have at most one link between them.

- **Blocked by**: the task builds on another's work, so its agent waits until that task is merged. **Blocks** is the same link seen from the other task.
- **Related to**: the tasks are shown on each other, and their agents are told about each other, but neither waits.

A task that's blocked shows **blocked by #3** on the list. **Start agent** on it becomes **Start when unblocked**: the task stays to do, shows **starts after #3**, and its agent starts by itself once every task it's blocked by is merged, on a branch made from the latest of the branch it starts from. **Don't start** takes that back. If the agent can't start then, for example because the task's budget is spent, the task stays to do and says why.

- Only a task that hasn't started can be blocked. Once its agent has started, its work no longer waits for anything.
- A task's merge request merges into the project's default branch, so only a task that starts from the default branch can be blocked: from any other branch, its agent would start without the work it waited for.
- A link that would make tasks wait for each other, directly or through other tasks, is refused.
- A cancelled task never merges, so it can't block a task, and a task already blocked by one that's then cancelled keeps waiting until you remove the link. Deleting a task removes its links, which can let a task it blocked start.

The agent of a linked task is told the titles and status of the tasks it's linked to: those it waited for, which are merged, those waiting for it, and related ones it should leave alone.

## Comments {#comments}

A task's **Comments**, below its description, are a discussion about the task. You, the task's agent and the [assistant](#the-assistant) can all comment, and each comment says who wrote it: **You**, **Task agent** or **Assistant**.

- The task's agent is told to read its task's comments when it starts and again before it finishes, so a comment can add to a task or change it without rewriting its description. Your comments carry your word. The agent weighs the assistant's against the task.
- The agent comments for you to see with the task: a question, a decision it made, or a problem it found outside the task. What a round did goes in its closing summary, as before.
- Each author can edit only its own comments: the pencil edits yours, and an agent can't touch yours or another agent's. No one can delete a comment. Deleting the task deletes its comments with it.
- Every version of a comment is kept. An edited comment shows **edited** and how many versions it has. Press it to see each one, from the **Original** to the **Current**. An agent's edited comment is highlighted, so you can check what it said before.
- So that nothing is hidden from you, HTML in a comment shows as the text it is, not rendered, and so does Markdown that would otherwise show nothing, such as a `[//]: # (...)` comment. A link's title and an image's description, which a screen otherwise shows only on hover, show in brackets beside them. An agent's comment can't contain characters a screen doesn't show, such as zero-width or bidi control characters, whether written as themselves or as HTML references like `&#x200B;`.

## How a task runs {#how-a-task-runs}

Each task works on a branch of its own, `task/<id>`, in a sandbox: a fresh workspace made from the project, with the project's environment, its own copy of each [service](/docs/services), and the setup script already run.

1. **Queued**: the task waits for a free sandbox. A project runs at most its **Concurrent sandboxes** at once.
2. **Agent working**: the agent edits, runs and commits in the sandbox. **Watch in sandbox** opens it to follow along. When the project has a [pipeline](/docs/pipelines), the agent is told its jobs and runs them before it finishes.
3. **In review**: when the agent finishes, devpad collects its branch and opens a merge request into the default branch, or moves the open one to the new commit. If the agent made no commits, the task fails instead. When the pipeline guarding the merge request fails, devpad [sends the task back to its agent](/docs/pipelines#failures) with what failed, up to twice in a row, and the task says **Sent back after the pipeline failed (1 of 2)**; after that the failure waits for you.
4. **Changes requested**: you or a review asked for changes, or a pipeline failure waits for you. **Send back to agent** sends the feedback, and the agent picks up where it left off, on the same branch.
5. **Merged** or **Cancelled**. A task whose merge request targets a branch devpad [merges into automatically](/docs/branches#automatic-merges) is merged as soon as a review agent approves it.

The sandbox stops after each round, freeing its slot, and keeps the branch and the agent's transcript for the next. **Cancel task** stops the agent and removes the sandbox; anything already published stays. A task that **Failed** can be started again with **Retry**, with another model if you like.

The agent has no remote and no credentials: it can't push, merge or touch a protected branch. Devpad collects its branch when it finishes.

Like the [assistant](#the-assistant), the task's agent and every review agent can read these docs, so they can find out how devpad treats their work, such as why it refused a push or what a merge request needs before it merges.

## Task budgets {#task-budgets}

A task's budget covers every run of its agent, across retries and rounds of feedback, and every review of its merge request. When it's spent, the agent stops after its current request and the task shows **Budget reached**. **Raise and continue** raises it and starts the agent again. See [Budgets](/docs/ai#budgets).

## The assistant {#the-assistant}

The **AI** panel is the assistant: an agent working in your project workspace, beside you. Pick the **Agent** (the **Coding Agent**, the **Code Review Agent**, or one you've made under **AI Agents**), the **Model** and **Effort** below the message box.

- It reads, edits and runs things in your workspace. It leaves its edits for you to review and commits only when you ask.
- In a project workspace it can see the board: list tasks and merge requests, create, start and [link](#linked-tasks) tasks, send a task back to its agent, read and leave [comments](#comments) on tasks, and comment on merge requests. It can add a link but never remove one, and edit only its own comments.
- It can open a merge request from a published branch into a protected branch it names. It can't open one from the branch of a task that isn't merged or cancelled, which gets its merge request when the task's agent finishes, and it can't merge, close or approve a merge request.
- It can publish a branch with `git push devpad`, with no more power than the Publish button.
- It can answer questions about devpad itself from these docs.
- Its replies, like all text in devpad, show only images devpad itself serves. An image from anywhere else shows as a link, **Image:** and its description, so that nothing an agent writes can make your browser send what's on the page to another site.
- **Hand off as task** turns the conversation into a new task, whose agent finishes the work in its own sandbox and opens a merge request. It starts from your work on the current branch, or from the project's latest default branch, and gets the conversation so far. The chat stays in the panel, and you can keep using it.

Each reply spends from your **Per assistant reply** budget, if you set one.

## Agent runs {#agent-runs}

**Agent Runs**, on the right of the IDE, lists the workspace's runs as one card per conversation with the assistant, the most recently active first. A card's header shows how many messages you sent and how many sub-agents worked on them, what the whole conversation cost, and **Running**, **Needs you** or **Failed** while one of its runs is.

- **You** marks each message you sent, with how the assistant's reply went (**Replied**, **Running**, **Needs approval**, **Needs input**, **Failed** or **Cancelled**), how long it took, its model and its cost, which includes its sub-agents'.
- Below a message, **Assistant started 2 sub-agents** leads to the sub-agents the assistant handed parts of that work to. Each shows its agent, the job it was given, how it went (**Done** rather than **Replied**) and what it cost. A sub-agent's own sub-agents sit below it the same way.
- The latest conversation opens by itself, and so does any that's running, waiting for you or holding the run you're looking at. Press a card's header to open or close it.
- Press a message or a sub-agent to read its run in the **AI** panel, and its stop button to stop it while it runs.

In a task's or review's sandbox, the messages are the rounds devpad sends the agent, marked **Task** or **Review**.

## Approvals {#approvals}

Some of an agent's tool calls wait for you before they run:

- any command that uses `sudo`;
- starting a background agent (creating and starting a task, or starting one) and sending a task back to its agent. For a task that's blocked, the card lists the tasks it **Waits for**, and the agent starts once they're merged;
- the assistant opening a merge request: the card shows the branch, the branch it would merge into, whether an automatic review would start, and any commits in your workspace that aren't published and so would be left out.

The call shows as a card with **Approve** and **Deny**, in the chat and in the inbox. An agent can also stop to ask you a question. Both appear in the **inbox** in the header, from every workspace and sandbox, so you can answer an agent without finding it first.

## Skills {#skills}

Skills are instructions a project keeps for particular kinds of work, in the [Agent Skills](https://agentskills.io) format: a directory per skill under `.devpad/skills/` or `.claude/skills/`, each with a `SKILL.md` whose front matter gives its `name` and `description`. A skill in `.devpad/skills/` wins over one of the same name in `.claude/skills/`.

- Every agent sees the skills' names and descriptions, and loads one when its work matches.
- Type `/` in the assistant to list them, and `/<name>` to start one yourself.
- Skills only add instructions: `allowed-tools` is ignored, and nothing in a skill lets an agent do more than it already can.

The repo's `AGENTS.md`, at its root, is given to every agent with its instructions. `/init` in the assistant writes or updates one for the project. A review agent takes `AGENTS.md` and skills from the branch the change would merge into, never from the change it's reviewing.
