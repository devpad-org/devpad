---
title: Tasks and agents
summary: Handing work to AI agents with tasks, the assistant in your workspace, approvals, and skills.
---

There are two ways to work with AI in devpad. A **task** hands a change to a background agent, which works on its own branch in a sandbox and comes back with a merge request. The **assistant** works with you, in your own workspace, one message at a time.

## Tasks {#tasks}

The **Tasks** surface lists the project's tasks, with what each has spent and its merge request. Press **New task**:

- **Title** and **Description**: what should change, and how you'll know it works. The agent sees both, with the project's description.
- **Start from**: the branch the task's own branch is made from. It must be in the project, so publish it first.
- **Agent**: the model and effort, which start from your [default models](/docs/ai#default-models).
- **Budget**: what agents may spend on the task across all their runs. Empty uses your default; 0 means no limit.

**Create and start agent** hands it to an agent straight away. **Save as to do** keeps it on the list to start later with **Start agent**.

## How a task runs {#how-a-task-runs}

Each task works on a branch of its own, `task/<id>`, in a sandbox: a fresh workspace made from the project, with the project's environment, its own copy of each [service](/docs/services), and the setup script already run.

1. **Queued**: the task waits for a free sandbox. A project runs at most its **Concurrent sandboxes** at once.
2. **Agent working**: the agent edits, runs and commits in the sandbox. **Watch in sandbox** opens it to follow along.
3. **In review**: when the agent finishes, devpad collects its branch and opens a merge request into the default branch, or moves the open one to the new commit. If the agent made no commits, the task fails instead.
4. **Changes requested**: you or a review asked for changes. **Send back to agent** sends the feedback, and the agent picks up where it left off, on the same branch.
5. **Merged** or **Cancelled**.

The sandbox stops after each round, freeing its slot, and keeps the branch and the agent's transcript for the next. **Cancel task** stops the agent and removes the sandbox; anything already published stays. A task that **Failed** can be started again with **Retry**, with another model if you like.

The agent has no remote and no credentials: it can't push, merge or touch a protected branch. Devpad collects its branch when it finishes.

## Task budgets {#task-budgets}

A task's budget covers every run of its agent, across retries and rounds of feedback, and every review of its merge request. When it's spent, the agent stops after its current request and the task shows **Budget reached**. **Raise and continue** raises it and starts the agent again. See [Budgets](/docs/ai#budgets).

## The assistant {#the-assistant}

The **AI** panel is the assistant: an agent working in your project workspace, beside you. Pick the **Agent** (the **Coding Agent**, the **Code Review Agent**, or one you've made under **AI Agents**), the **Model** and **Effort** below the message box.

- It reads, edits and runs things in your workspace. It leaves its edits for you to review and commits only when you ask.
- In a project workspace it can see the board: list tasks and merge requests, create and start tasks, send a task back to its agent, and comment on merge requests.
- It can open a merge request from a published branch into a protected branch it names. It can't open one from the branch of a task that isn't merged or cancelled, which gets its merge request when the task's agent finishes, and it can't merge, close or approve a merge request.
- It can publish a branch with `git push devpad`, with no more power than the Publish button.
- It can answer questions about devpad itself from these docs.
- **Continue in background** hands the conversation to a background agent as a new task, in its own sandbox. It starts from your work on the current branch, or from the project's latest default branch, and gets the conversation so far.

Each reply spends from your **Per assistant reply** budget, if you set one.

## Approvals {#approvals}

Some of an agent's tool calls wait for you before they run:

- any command that uses `sudo`;
- starting a background agent (creating and starting a task, or starting one) and sending a task back to its agent;
- the assistant opening a merge request: the card shows the branch, the branch it would merge into, whether an automatic review would start, and any commits in your workspace that aren't published and so would be left out.

The call shows as a card with **Approve** and **Deny**, in the chat and in the inbox. An agent can also stop to ask you a question. Both appear in the **inbox** in the header, from every workspace and sandbox, so you can answer an agent without finding it first.

## Skills {#skills}

Skills are instructions a project keeps for particular kinds of work, in the [Agent Skills](https://agentskills.io) format: a directory per skill under `.devpad/skills/` or `.claude/skills/`, each with a `SKILL.md` whose front matter gives its `name` and `description`. A skill in `.devpad/skills/` wins over one of the same name in `.claude/skills/`.

- Every agent sees the skills' names and descriptions, and loads one when its work matches.
- Type `/` in the assistant to list them, and `/<name>` to start one yourself.
- Skills only add instructions: `allowed-tools` is ignored, and nothing in a skill lets an agent do more than it already can.

The repo's `AGENTS.md`, at its root, is given to every agent with its instructions. `/init` in the assistant writes or updates one for the project. A review agent takes `AGENTS.md` and skills from the branch the change would merge into, never from the change it's reviewing.
