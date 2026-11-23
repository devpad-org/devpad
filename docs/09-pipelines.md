---
title: Pipelines
summary: The jobs a project runs on its protected branches and merge requests, where they come from, how they run, and what a result means.
---

A pipeline runs a project's jobs, such as its tests and its build, on a commit of a protected branch or on a merge request, each job in a fresh container that no agent can reach. Its result tells you whether the change builds and passes before you read it.

Devpad runs pipelines by itself on the protected branches you tick under **Run pipelines**, on each new commit, and on every merge request into them, when it opens and each time its branch or the ticked branch moves. A merge request into a ticked branch [merges only once its pipeline passes](#merge-gate), unless you merge it anyway. You can run a pipeline on any protected branch, and on any merge request into one, yourself; into a branch you don't tick, its result is there for you to weigh and doesn't stop anything from merging.

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

- **jobs** lists up to 20 jobs, shown in the file's order. A job's name is up to 40 lowercase letters, digits, `-` and `_`, and starts with a letter or digit.
- **run** is the job's bash script. It stops at the first command that fails.
- **timeout** is how long the job may run, such as `90s` or `15m`: 10 minutes when left out, and at most an hour.
- **services** lists up to 8 of the project's [services](/docs/services#project-services) by type, such as `postgres`. The job gets fresh copies of its own.

Devpad reads the file strictly: a key it doesn't know is an error, so a misspelt `script:` fails rather than quietly running nothing. The file may be at most 64 KB. A file that doesn't parse makes the pipeline **Invalid**, with the error and its line; devpad never falls back to the project's setting then, which would quietly run other jobs.

## The project's pipeline setting {#project-setting}

For a repo where committing a devpad file isn't welcome, such as one you don't own, the **Pipeline setting** card on the Project page holds the same YAML for the project. Only you can change it: no agent or assistant can. Devpad checks it as you save it and says on which line anything is wrong; leave it empty to have none.

The setting applies only to a commit that has no pipeline file, `.devpad/pipeline.yml` or `.devpad/pipeline.yaml`. When the file is there, it wins (both at once make the pipeline **Invalid**), and the two are never combined. One setting covers every branch, so every protected branch gets the same jobs from it. With neither a file nor the setting, there is no pipeline, and devpad says **No pipeline configured**, never that the commit passed.

## Which branches run pipelines {#branches}

Only protected branches and merge requests into them run pipelines. Anyone who can push to an unprotected branch can rewrite its pipeline file, an agent as often as not, so its result would prove nothing.

- **A protected branch's pipeline** tests the branch's current commit, with the pipeline file in that commit.
- **A merge request's pipeline** tests its branch merged onto the target branch's current commit, so it also catches two merge requests that pass alone but break once both land. Its jobs come from the target branch's pipeline file, never the change's, so a task's agent can't rewrite the jobs that grade its work. A branch that doesn't merge cleanly makes the pipeline **Doesn't merge** without running anything.

Under **Run pipelines** in the **Branches** dialog, tick the protected branches devpad [runs pipelines on by itself](#automatic-runs). The Project page shows the result of each ticked branch's latest pipeline beside it, skipping pipelines that were cancelled, replaced, interrupted or couldn't run, and says **Out of date** once the branch has moved past it. When the last pipeline of the branch's current commit was cancelled or couldn't run, it says **Cancelled** or **Couldn't run** instead, and why when you point at it. A protected branch you don't tick, and the merge requests into it, run pipelines only when you start them, and no pipeline guards what merges into it.

To run a pipeline, press **Run pipeline** on the **Pipelines** surface and pick a protected branch, or press **Run pipeline** on a merge request's **Pipeline** card (**Run pipeline again** once it has one). Starting one that is already queued or running for the same commits shows that one instead. **Run again** on a finished pipeline runs its branch or merge request again at the commits it is at now, and **Cancel** stops a queued or running one.

## When devpad runs a pipeline {#automatic-runs}

A pipeline's result counts only for the commits and jobs it tested, so devpad runs one again by itself whenever they change for a ticked branch or a merge request into one:

- **A ticked branch** runs its new commit after each merge into it and each fast-forward from the upstream, the only ways a protected branch moves, and its current commit when you tick it.
- **A merge request into a ticked branch** runs when it opens, whether a task's agent, you or the assistant opened it; when its branch gets new commits, from a task's agent finishing a round, a publish or `git push devpad` from your workspace, or, for a release such as `develop` into `main`, a merge into `develop` or a fast-forward of `develop` from the upstream; and when the ticked branch moves, since it then merges onto another commit. Ticking its target runs it too.
- **Changing the pipeline setting** runs again the ticked branches and merge requests whose jobs come from it. Those with a pipeline file get the same jobs, so nothing runs for them.

A pipeline devpad starts by itself says what started it, such as **Started when develop moved**; one you start says **Run by you**.

Devpad starts a branch's or a merge request's pipeline at most once every 30 seconds. When it moves again sooner, as when an agent pushes several times in a row, devpad waits until the 30 seconds are up and then runs its newest commits. What you do yourself, ticking a branch or changing the setting, runs at once.

When a ticked branch moves again before its pipeline started, the newer commit's pipeline **Replaces** it; one already running finishes, so the branch's history keeps the result of each pipeline devpad ran. A merge request's pipeline that is out of date is replaced even while it runs, since only the newest result can count. Closing or merging a merge request stops its pipelines, and unticking a branch stops the pipelines devpad started of it and of the merge requests into it that are still queued; each says why it was **Replaced**.

Devpad never runs a pipeline because another ended. It doesn't run again by itself a pipeline you cancelled, on the same commits and jobs, and runs one that couldn't run once more within 10 minutes, then leaves it to you. Until you press **Run again** or the commits change, the merge request's **Pipeline** card and merge card, and a ticked branch's badge, say **Pipeline cancelled** or **Pipeline couldn't run**, with why. As devpad starts, and every 10 minutes after, it runs any ticked branch or merge request into one that has no pipeline of its commits and jobs as they are, such as one that moved while devpad was down, and **Started when devpad caught up** says so.

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
- **Cancelled**, **Replaced** by a newer pipeline of the same branch before it ran, or of the same merge request, or because it no longer had anything to test, or **Interrupted** by devpad stopping.

A job is queued, running, passed, failed, timed out, out of memory, error, cancelled, interrupted, or skipped when its pipeline ended before it ran.

## Merge requests {#merge-requests}

A merge request's result counts only for what it tested: its branch at its current commit, merged onto the target branch's current commit, with the jobs the target's pipeline has now. A pipeline of an older commit of either, or of jobs that have changed since, is out of date, and devpad works out which on its side. Into a ticked branch, devpad [runs the pipeline again](#automatic-runs) whenever that happens; into another protected branch, run it from the merge request.

A merge request's **Pipeline** card shows the pipeline that counts now, or else its latest, saying when that is out of date, with its jobs and where they came from. The merge card has a row for it: **Pipeline passed** only for a pass that counts now, **Pipeline out of date** for an older result, and **No pipeline configured** when the target branch has no pipeline file and the project no setting. The activity lists each pipeline the merge request ran, and picking one there shows it on the card. The list of merge requests shows each open one's latest result beside its status, and **Out of date** once its branch has moved past it; open the merge request to see whether its target or jobs have too. Into a ticked branch, the result decides whether the merge request can merge: see [The merge gate](#merge-gate). Into another protected branch, it is there for you to weigh.

## The merge gate {#merge-gate}

A pipeline guards a merge request when its target is ticked under **Run pipelines** and has a pipeline: a pipeline file on the target's current commit, or the project's setting. A pipeline file that doesn't parse counts too, so a broken file keeps merge requests from merging rather than letting them through. A guarded merge request merges only once its pipeline **passed** on its branch merged onto the target's current commit, with the jobs the target has now, and devpad then merges it only while the target is still at that commit, and only if the merge lands the very tree the pipeline tested, so the tree that passed is the tree that lands, whichever merge strategy you pick. If the target moves as you merge, devpad refuses and the pipeline runs again on the target's new commit.

Until then the merge card's pipeline row says what keeps it from merging: the pipeline is queued or still running, none has run yet, the latest is out of date, it failed, found the branch doesn't merge, doesn't parse, couldn't run or was cancelled. A merge request into a branch you don't tick, or whose target has no pipeline, isn't guarded: the merge card says **No pipeline guards &lt;target&gt;**, and nothing but you, or a review agent's approval for an [automatic merge](/docs/branches#automatic-merges), decides what merges.

**Merge anyway.** You can merge a guarded merge request whose pipeline hasn't passed: **Merge into &lt;target&gt;…** asks you to confirm, naming how the pipeline stands, and **Merge anyway** merges it. What you confirm is that pipeline on those commits: if the pipeline, the branch or the target changes while you decide, even to another pipeline that failed the same way, devpad asks again with how it stands now. A branch that doesn't merge onto its target can't be merged anyway, since the merge would conflict just as the pipeline's did: the merge card lists the conflicting files, and on a task's merge request **Ask the task's agent to resolve them** has the agent merge the target in, after which the pipeline runs again. The merge request then says you merged it although its pipeline hadn't passed, and how it stood, such as **by you although its pipeline failed (#12)**. Only you can do this, from the merge card: agents and the assistant have no way to merge at all, and devpad never merges automatically over the gate.

An [automatic merge](/docs/branches#automatic-merges) needs the pass too, and never takes a change that adds, changes, renames or deletes the pipeline file itself: the target's jobs would test it, but once merged it would decide what every later merge request has to pass, so you merge it yourself. When the review agent's approval comes before the pipeline has finished, the merge waits for it, and the merge card says **Merging into &lt;target&gt; once the pipeline passes**; devpad merges once it passes, and records why not when it fails, can't run or is cancelled.

## What pipelines don't stop {#limits}

Reading the jobs from the target branch makes cheating visible, not impossible. A change can still weaken what the jobs check:

- delete a test, or skip one with `t.Skip` or `it.skip`;
- change what a command does, such as a `Makefile` target or a script in `package.json`;
- loosen a type-check or lint configuration.

Review a change's tests and build files as closely as its code.
