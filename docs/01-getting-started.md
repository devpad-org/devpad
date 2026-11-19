---
title: Getting started
summary: What devpad is, the first things to set up, and a tour of the dashboard.
---

Devpad is a self-hosted IDE in the browser. Each **project** gets a full workspace for you (editor, terminal, git, previews) and a board of **tasks** that AI agents work on in parallel, each in a sandbox of its own, handing their work back as **merge requests** for you to review and merge.

## First steps {#first-steps}

1. **Add an AI provider key** under **Settings > Provider Keys**, if you want to use agents. Agents run on your own key, so the provider bills you. See [Provider keys](/docs/ai#provider-keys).
2. **Generate an SSH key** under **Settings > Git** if your project's repository is private or you want to push merges back to it, and set the name and email your commits should carry. See [Your SSH key](#ssh-key) and [Your Git identity](#git-identity).
3. **Create a project** from the dashboard with **New project**: give it a name and, optionally, the repository to clone. See [Creating a project](/docs/projects#creating-a-project).
4. **Open the project** to land in its IDE, where your workspace is ready to use.
5. **Give an agent a task** from the **Tasks** board, or ask the assistant in the **AI** panel. See [Tasks and agents](/docs/tasks).

## Your SSH key {#ssh-key}

Devpad uses your SSH key to clone a project's upstream, sync from it, and push merged work back to it. Add the public key from **Settings > Git** to your Git host (GitHub, GitLab, Bitbucket…), and give projects an SSH URL such as `git@github.com:org/repo.git`: the key doesn't work with `https://` URLs.

The key stays in the project's repo container. Your project workspace and agents' sandboxes never get it: your workspace reaches the project's repo through devpad instead (see [How devpad works](/docs/how-it-works#where-code-lives)). Only workspaces made before projects existed still get the key.

## Your Git identity {#git-identity}

Your commits carry the **Name** and **Email** under **Settings > Git**. Leave either empty to use your username or account email. Anything you push upstream shows them, so you may prefer your full name and a private address, such as the noreply address GitHub gives you.

They apply to:

- your commits in project workspaces;
- the commits of agents working on your tasks and reviews, which are made in your name;
- the merge commits devpad makes when you merge.

When you save, running project workspaces and sandboxes change straight away, and stopped ones change when they next start. This replaces any `user.name` or `user.email` you set by hand with `git config` in those repositories. Commits already made keep the identity they were made with. Workspaces made before projects keep their own.

Until your first save, devpad leaves those settings alone, and saving with both fields empty applies your username and account email. From then on, devpad applies your identity again each time a project workspace or sandbox starts, restarts or is rebuilt, so a value you set by hand lasts only until then. If an admin changes your username or email and you left that field empty, your running workspaces change straight away.

## The dashboard {#dashboard}

The dashboard lists your projects, most recently active first. Each card shows the project's upstream (or **local only**), its default branch, and what's going on: tasks in progress, agents running, open merge requests, and how many things **need you**, such as an agent waiting for an answer or a merge request to review.

If you have workspaces from before projects, they're listed under **Workspaces to convert**. See [Standalone workspaces](/docs/projects#standalone-workspaces).

## The inbox {#inbox}

The inbox icon in the header (and in the IDE's title bar) counts the agents waiting for you, across all your projects: an agent that wants your approval to run a command or start another agent, or that has asked you a question. Open it to approve, deny or answer each one right there, or to jump to its chat, task or merge request. See [Approvals](/docs/tasks#approvals).

## Getting help in the IDE {#help}

The small **?** links throughout devpad open the page of these docs that explains what's beside them, in a new tab so you don't lose your place. You can also ask the assistant in a project's **AI** panel how devpad works: it reads these same pages.
