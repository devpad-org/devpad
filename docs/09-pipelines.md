---
title: Pipelines
summary: The jobs a project runs on its protected branches and merge requests, where they come from, how they run, and what a result means.
---

A pipeline runs a project's jobs, such as its tests and its build, on a commit of a protected branch or on a merge request, each job in a fresh container that no agent can reach. Its result tells you whether the change builds and passes before you read it.

For now, pipelines run only when you start them: devpad doesn't yet start one on its own when a branch moves or a merge request opens, and a pipeline's result doesn't stop anything from merging.

## The pipeline file {#pipeline-file}

A project's pipeline lives in its repo, in `.devpad/pipeline.yml` (or `.devpad/pipeline.yaml`, but not both). It names the jobs and the script each runs:

```yaml
jobs:
  backend:
    run: go test ./...
    timeout: 15m
    services: [postgres]
  frontend:
    run: |
      cd frontend
      npm ci
      npm run type-check
```

- **jobs** lists up to 20 jobs, shown in the file's order. A job's name uses lowercase letters, digits, `-` and `_`, and starts with a letter or digit.
- **run** is the job's bash script. It stops at the first command that fails.
- **timeout** is how long the job may run, such as `90s` or `15m`: 10 minutes when left out, and at most an hour.
- **services** lists up to 8 of the project's [services](/docs/services#project-services) by type, such as `postgres`. The job gets fresh copies of its own.

Devpad reads the file strictly: a key it doesn't know is an error, so a misspelt `script:` fails rather than quietly running nothing. The file may be at most 64 KB. A file that doesn't parse makes the pipeline **Invalid**, with the error and its line; devpad never falls back to the project's setting then, which would quietly run other jobs.

## The project's pipeline setting {#project-setting}

For a repo where committing a devpad file isn't welcome, such as one you don't own, the **Pipeline setting** card on the Project page holds the same YAML for the project. Only you can change it: no agent or assistant can. Devpad checks it as you save it and says on which line anything is wrong; leave it empty to have none.

The setting applies only to a commit that has no `.devpad/pipeline.yml`. When the file is there, it wins, and the two are never combined. One setting covers every branch, so every protected branch gets the same jobs from it. With neither a file nor the setting, there is no pipeline, and devpad says **No pipeline configured**, never that the commit passed.

## Which branches run pipelines {#branches}

Only protected branches and merge requests into them run pipelines. Anyone who can push to an unprotected branch can rewrite its pipeline file, an agent as often as not, so its result would prove nothing.

- **A protected branch's pipeline** tests the branch's current commit, with the pipeline file in that commit.
- **A merge request's pipeline** tests its branch merged onto the target branch's current commit, so it also catches two merge requests that pass alone but break once both land. Its jobs come from the target branch's pipeline file, never the change's, so a task's agent can't rewrite the jobs that grade its work. A branch that doesn't merge cleanly makes the pipeline **Doesn't merge** without running anything.

Under **Run pipelines** in the **Branches** dialog, tick the protected branches whose pipelines you follow. The Project page shows the result of each ticked branch's latest pipeline beside it. Ticking a branch doesn't start anything yet.

To run a pipeline, press **Run pipeline** on the **Pipelines** tab and pick a protected branch, or press **Run pipeline** on a merge request's **Pipeline** card (**Run pipeline again** once it has one). Starting one that is already queued or running for the same commits shows that one instead. **Run again** on a finished pipeline runs its branch or merge request again at the commits it is at now, and **Cancel** stops a queued or running one.

## How jobs run {#how-jobs-run}

Each job runs in a container of its own, made from the project's [environment](/docs/projects#environment) image, with a clone of the commit under test in `/workspace`. Jobs don't share anything: one job can't leave a changed tool or file behind for another. The container has:

- no agent, no AI and nothing of devpad's: no token, no SSH keys and none of the project's settings, only the connection variables of the job's own services;
- the `devpad` user with no way to become root: `sudo` is refused. Install the tools jobs need in the project's environment;
- a network of its own with internet access, which nothing else joins, and up to 4 GB of memory and 2 CPUs;
- fresh copies of the services the job names, each ready before the job starts and removed with it.

The project's setup script doesn't run: a job's `run` does its own preparation, such as `npm ci`. A pipeline waits for an environment build in progress and records the image it ran on. All of a pipeline's jobs run, in parallel, even when one fails, and a project runs at most 3 jobs at once across its pipelines; the rest wait their turn. Devpad decides each job's result from how its script exited, never from what it printed.

A job's output shows live while it runs. Devpad keeps the last 1 MB of each job's output, and the output of the project's 50 newest pipelines. Devpad's own notes on a job, such as starting its services and how it ended, show apart from the job's output, marked as devpad's, so nothing a job prints can pass for one. If devpad stops while a pipeline runs, the pipeline is marked **Interrupted** and devpad runs it again once it starts.

## Statuses {#statuses}

A pipeline is:

- **Queued**: waiting for the environment or for a job to finish elsewhere in the project;
- **Running**: its jobs are being set up or are running;
- **Passed**: every job passed;
- **Failed**: a job failed, ran out of time or ran out of memory;
- **Error**: devpad couldn't run it, or couldn't set up a job, such as one naming a service the project doesn't have;
- **Invalid**: its pipeline file or setting doesn't parse;
- **Doesn't merge**: the merge request's branch conflicts with its target;
- **No pipeline**: the commit has no pipeline file and the project no setting;
- **Cancelled**, **Replaced** by a newer pipeline of the same branch or merge request before it ran, or **Interrupted** by devpad stopping.

A job is queued, running, passed, failed, timed out, out of memory, error, cancelled, interrupted, or skipped when its pipeline ended before it ran.

## Merge requests {#merge-requests}

A merge request's **Pipeline** card shows its latest pipeline, its jobs and where they came from, and the merge card has a row for it: **No pipeline configured** when the target branch has no pipeline file and the project no setting. The activity lists each pipeline the merge request ran, and picking one there shows it on the card. The list of merge requests shows each open one's latest result beside its status. A pipeline that ran on an older commit than the merge request's says so. A pipeline's result doesn't block merging yet: it is there for you to weigh.

## What pipelines don't stop {#limits}

Reading the jobs from the target branch makes cheating visible, not impossible. A change can still weaken what the jobs check:

- delete a test, or skip one with `t.Skip` or `it.skip`;
- change what a command does, such as a `Makefile` target or a script in `package.json`;
- loosen a type-check or lint configuration.

Review a change's tests and build files as closely as its code.
