---
title: How devpad works
summary: The pieces devpad is made of, where your code lives, and why agents can't touch what they shouldn't.
---

You only need this page if you want to know why devpad behaves as it does. Everything here follows from one idea: **AI agents run code you haven't read, so every agent works in a box of its own and devpad decides what leaves it.**

## The pieces {#the-pieces}

```
                     +----------------------------------+
  your browser  -->  | devpad server                    |
                     | web app, API, AI agents' model   |
                     | calls, database                  |
                     +----------------------------------+
                         | Docker             | git bundles
  +----------------------|--------------------|-------------------+
  | one project          v                    v                   |
  |  +------------------------+   +-------------------------+     |
  |  | project workspace      |   | repo container          |     |
  |  | (your IDE) + agent     |   | canonical bare repo,    |-----|--> upstream
  |  +------------------------+   | your SSH key            |     |    (GitHub...)
  |  +------------------------+   +-------------------------+     |
  |  | sandbox: a task or a   |                                   |
  |  | review + agent         |   ... one per task or review      |
  |  +------------------------+                                   |
  +---------------------------------------------------------------+
```

- **The devpad server** serves this web app and its API, keeps everything in a SQLite database, and drives Docker. It is also where AI agents' model requests come from: the model is called from the server with your provider key, and only the agent's tool calls (read a file, run a command) happen inside a workspace.
- **A workspace** is a Docker container with your code in `/workspace`, its own Docker volume and its own network. A small program devpad puts in every workspace, the *agent*, gives the server its terminals, files, commands and git. (This "agent" is plumbing, not an AI.)
- **A project** groups everything about one codebase: its **canonical repo**, your **project workspace**, and a **sandbox** for each AI task or review.
- **The repo container** holds the project's canonical bare git repo. It is the only container with your SSH key and the only one that talks to the upstream (GitHub, GitLab…).

## Where your code lives {#where-code-lives}

The canonical repo is the project's source of truth. No workspace mounts it, can reach it over the network, or holds a credential for it. Instead, devpad itself moves commits in and out as git bundles:

- **Into a workspace**: when a workspace is made, devpad seeds it from the canonical repo. In your project workspace, `git fetch devpad` brings newer branches in.
- **Out of a workspace**: when you press **Publish** (or run `git push devpad <branch>`), devpad pulls the branch out of your workspace, checks it against the project's rules, and writes it to the canonical repo. A task's agent hands its branch over the same way.
- **To and from the upstream**: devpad syncs from the upstream when asked, and pushes protected branches to it when you choose to, from the repo container with your SSH key. See [Upstream](/docs/projects#upstream).

Because devpad does the moving, it can refuse: a push to a protected branch, a force-push, a branch an agent is working on. Those rules live in the server, out of reach of anything running in a workspace. See [Branches and merge requests](/docs/branches).

## Sandboxes and isolation {#isolation}

Each task and each review gets a sandbox: a fresh workspace seeded from the canonical repo, owning exactly one branch. Its agent can edit and run anything inside it, but:

- it has no SSH key, no route to the canonical repo, and no way to reach your project workspace or other sandboxes;
- it can only change its own branch, and only by handing commits to devpad;
- it can't merge, close a merge request, push upstream or move a protected branch: those stay with you.

Your project workspace has no SSH key either. Its `devpad` and `origin` remotes look like ordinary git remotes, but they're served by devpad, which applies the same rules as the Publish button.

## AI runs {#ai-runs}

Every AI run, whether the assistant answering you, a task's agent or a review, is the same loop: the server sends the conversation to the model with your key, the model asks for tool calls, and the server runs them in the run's workspace (or on the host, for tools like the task board that only devpad can answer). A run stops when it's done, when its budget is spent, or after 500 model requests. See [AI settings and usage](/docs/ai).

Runs go on without your browser, and the server saves each conversation as the run goes, one step at a time. Closing the tab, losing your connection, cancelling a run or a run failing all keep what it had done, and your next message picks up from there. A conversation answers one message at a time: while a run is working in it, another message to it is refused until the run ends.

The tools a run gets depend on where it runs: the assistant in your project workspace can see the board, create tasks and open merge requests, a review agent gets review tools, and so on. A run's instructions include the project's `AGENTS.md` and [skills](/docs/tasks#skills); a review takes both from the branch the change would merge into, so the change under review can't tell its reviewer how to review it.

## Environments {#environments}

Every workspace starts from devpad's own image, `devpad-workspace:latest`. A project can add to it with an [environment](/docs/projects#environment), the body of a Dockerfile that devpad builds into an image for that project, and a [setup script](/docs/projects#setup-script) that runs once in each new workspace. [Services](/docs/services) such as a database run as separate containers beside each workspace, a fresh copy for every workspace.
