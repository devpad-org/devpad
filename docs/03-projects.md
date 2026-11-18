---
title: Projects and the IDE
summary: Creating a project, your project workspace, environments, setup scripts, the upstream, and previews.
---

A project is one codebase: its repository, the environment its workspaces are made from, and the agents working on it. You work in its **project workspace**; agents work in sandboxes of their own (see [Tasks and agents](/docs/tasks)).

## Creating a project {#creating-a-project}

Press **New project** on the dashboard.

| Field | What it's for |
|---|---|
| **Name** | The project's name. |
| **Description** | Optional. Agents see it with every task, so say what the project is. |
| **Upstream** | Optional. The repository to clone, such as `git@github.com:org/repo.git`, which devpad clones once with your [SSH key](/docs/getting-started#ssh-key) and can push merges back to. Leave it empty for a scratch project that starts from an empty repo. |
| **Default branch** | The branch merges land on. Leave it empty to use the upstream's default, or `main`. |
| **Concurrent sandboxes** | How many task and review sandboxes may run at once (1–20). Tasks beyond it wait in the queue. |
| **Review new merge requests automatically** | Start a review agent on each new merge request. See [Reviews](/docs/branches#reviews). |
| **Environment** | Optional Dockerfile lines for the project's image. See [Environment](#environment). |
| **Setup script** | Optional commands to run in each new workspace. See [Setup script](#setup-script). |

You can change everything but the default branch later with **Edit project** on the IDE's **Project** page; the default and protected branches are set with **Manage branches** (see [Protected branches](/docs/branches#protected-branches)). **Delete project**, at the bottom of the same page, deletes the project, its workspace, its sandboxes and its repository: branches that weren't pushed upstream are lost.

## The IDE {#the-ide}

Opening a project opens its IDE. The activity bar on the left switches between:

- **AI**: the [assistant](/docs/tasks#the-assistant), working in your workspace with you.
- **Explorer**: the files in `/workspace`.
- **Source Control**: changes, commits, branches, and [publishing](/docs/branches#publishing).
- **Tasks**, **Merge requests** and **Agents**: the board, the project's merge requests, and the agents' runs.
- **Workspace Info**: the container's status and processes, with **Restart** and **Stop**.
- **Database Services**: the workspace's [services](/docs/services).
- **Project**: a page of its own with the project's open tasks, merge requests and sandboxes at a glance, its protected branches and upstream, and how new workspaces are made: its environment, services and setup script, in that order.

The terminal is at the bottom. On a phone, the same panels are in the tab bar and its **More** menu.

If the project has no workspace yet, or it's stopped, the IDE offers to create or start it.

## Your project workspace {#project-workspace}

Your project workspace is a container with a git checkout of the project in `/workspace`. Work in it as you would locally: edit, run, commit, switch branches. Two git remotes connect it to the project:

- **`devpad`** is the project. `git fetch devpad` brings its branches in as `devpad/<branch>`, and `git push devpad <branch>` [publishes](/docs/branches#publishing) a branch. A plain `git pull` and `git push` use it.
- **`origin`**, when the project has an upstream, is a read-only view of the upstream. See [Upstream](#upstream).

Your workspace has no SSH key and no direct access to the project's repo: devpad moves commits in and out, and applies the project's rules as it does. See [How devpad works](/docs/how-it-works#where-code-lives).

## Environment {#environment}

A project's environment is the body of a Dockerfile, run as root on devpad's workspace image (`FROM devpad-workspace:latest`). Use it for toolchains and system packages, such as `RUN apt-get install -y …` or a language runtime; install the project's own dependencies in the [setup script](#setup-script) instead.

- `FROM`, `ADD` and `COPY` aren't allowed: devpad supplies the base image, and there are no files to copy. Download what you need with `RUN curl`.
- Devpad builds it in the background whenever it changes. Builds run on a network of their own, limited to 4 GB of memory, 2 CPUs and 30 minutes.
- The **Environment** card on the Project page shows the Dockerfile lines and whether they're **Built**, **Building…**, **Out of date** or **Build failed**, with **Show build log**. **Build again** retries a failed build.
- New workspaces get the last version that built, or devpad's plain image if none has.

## Rebuilding your workspace {#rebuilding}

Changing the environment doesn't touch your running workspace. When it's out of date, the Environment card offers **Rebuild workspace**, which replaces its container with one made from the current environment.

Everything in `/workspace` is kept, including uncommitted changes. Everything else is lost: packages you installed by hand, files in your home directory, and running processes. Rebuilding also gives your workspace any [project services](/docs/services#project-services) it doesn't have yet.

## Setup script {#setup-script}

The setup script runs once in each newly made workspace, in `/workspace`, after the repo is checked out: in your project workspace and in every task and review sandbox. Use it for the project's dependencies (`npm ci`, `go mod download`) and anything else a fresh checkout needs.

- It waits for the workspace's [services](/docs/services#readiness) to be ready, so it can create tables or load data.
- Agents start once it finishes, and are told if it failed.
- It can run for up to 30 minutes.
- The **Setup script** card on the Project page shows the script and how your workspace's run went (**Succeeded**, **Failed**, or **Waiting…** for the services to be ready) with **Show output**, and **Run again** reruns it.

## Upstream {#upstream}

A project's upstream is the repository on GitHub, GitLab or elsewhere that it was cloned from. Devpad reaches it from the project's repo container with your SSH key; workspaces never do.

- **Syncing** fetches the upstream and fast-forwards the project's protected branches that are strictly behind it. It never creates, deletes or force-moves a branch. Sync with **Sync from upstream** in the **Protected branches** card on the Project page, or with `git fetch origin` in your workspace; `git fetch devpad` syncs too when the last sync is over a minute old.
- **Pushing** sends a protected branch upstream: tick **Push … upstream** when merging, or press **Push** beside the branch in the Protected branches card. Only protected branches are ever pushed.
- In your workspace, `origin` shows the upstream's branches as `origin/<branch>`. Pushing to it is refused: publish to `devpad` and let devpad push upstream.
- If a protected branch has diverged from the upstream, merge `origin/<branch>` into a branch in your workspace and open a merge request into it.

You can add, change or remove the upstream with **Edit project**. Changing it forgets what was synced from the old one; removing it makes the project local only.

## Previews {#previews}

To see a web app running in your workspace, press **Open preview in new tab** in the IDE's title bar (**Preview** in the phone's More menu), enter the port it listens on, and press **Open Preview**. Devpad opens it at its own address, `https://<workspace>-<port>.<preview domain>`, through a one-time link that signs the new tab in to that preview for 24 hours.

Previews need the server to be set up with a preview domain; ask your admin if they don't open. If the new tab doesn't appear, allow pop-ups for devpad.

## Standalone workspaces {#standalone-workspaces}

Workspaces made before projects existed are listed on the dashboard under **Workspaces to convert**. You can still open and use them, but devpad no longer makes new ones. **Convert to project** turns one into a project, so you get a board, agents and merge requests:

- **Repository at `/workspace`, or no repository yet**: the workspace is converted in place and becomes the project's workspace. Devpad removes your SSH key from it, offers to strip other credentials it finds, restarts it, and replaces your SSH key, since an AI may have read the old one. Add the new public key to your Git host and remove the old one.
- **Repository in a subdirectory**: its branches seed a new project with a fresh workspace, and the original workspace is left as it was. Delete it once you've moved what you need.
