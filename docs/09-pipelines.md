---
title: Pipelines
summary: The jobs a project runs on its protected branches and merge requests, where they come from, how they run, and what a result means.
---

A pipeline runs a project's jobs, such as its tests and its build, on a commit of a protected branch or on a merge request, each job in a fresh container that no agent can reach. Its result tells you whether the change builds and passes before you read it.

Devpad runs pipelines by itself on the protected branches you tick under **Run pipelines**, on each new commit, and on every merge request into them, when it opens and each time its branch or the ticked branch moves. A merge request into a ticked branch [merges only once its pipeline passes](#merge-gate), unless you merge it anyway. On a task's merge request, a failure [goes back to the task's agent](#failures), and the automatic review waits for a pass. You can run a pipeline on any protected branch, and on any merge request into one, yourself; into a branch you don't tick, its result is there for you to weigh and doesn't stop anything from merging.

A merge request's jobs always come from its target branch, so a change can't rewrite the jobs that grade it. A change to the pipeline file itself runs the jobs it proposes too, [for information only](#proposed), and devpad [marks a change](#look-closely) that deletes tests or changes the files jobs depend on, which no pipeline result can vouch for.

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

Under **Run pipelines** in the **Branches** dialog, tick the protected branches devpad [runs pipelines on by itself](#automatic-runs). The Project page shows the result of each ticked branch's latest pipeline beside it, skipping pipelines that were cancelled, replaced, interrupted or couldn't run, and says **Out of date** once the branch has moved past it or the jobs it gets have changed. When the last pipeline of the branch's current commit was cancelled or couldn't run, it says **Cancelled** or **Couldn't run** instead, and why when you point at it. A protected branch you don't tick, and the merge requests into it, run pipelines only when you start them, and no pipeline guards what merges into it.

To run a pipeline, press **Run pipeline** on the **Pipelines** surface and pick a protected branch, or press **Run pipeline** on a merge request's **Pipeline** card (**Run pipeline again** once it has one). Starting one that is already queued or running for the same commits shows that one instead. **Run again** on a finished pipeline runs its branch or merge request again at the commits it is at now, and **Cancel** stops a queued or running one.

## When devpad runs a pipeline {#automatic-runs}

A pipeline's result counts only for the commits and jobs it tested, so devpad runs one again by itself whenever they change for a ticked branch or a merge request into one:

- **A ticked branch** runs its new commit after each merge into it and each fast-forward from the upstream, the only ways a protected branch moves, and its current commit when you tick it.
- **A merge request into a ticked branch** runs when it opens, whether a task's agent, you or the assistant opened it; when its branch gets new commits, from a task's agent finishing a round, a publish or `git push devpad` from your workspace, or, for a release such as `develop` into `main`, a merge into `develop` or a fast-forward of `develop` from the upstream; and when the ticked branch moves, since it then merges onto another commit. Ticking its target runs it too.
- **Changing the pipeline setting** runs again the ticked branches and merge requests whose jobs come from it. Those with a pipeline file get the same jobs, so nothing runs for them.

A merge request whose change touches the pipeline file runs [the pipeline it proposes](#proposed) at the same times. A pipeline devpad starts by itself says what started it, such as **Started when develop moved**; one you start says **Run by you**.

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

A merge request's **Pipeline** card shows the pipeline that counts now, or else its latest, saying when that is out of date, with its jobs and where they came from. The merge card has a row for it: **Pipeline passed** only for a pass that counts now, **Pipeline out of date** for an older result, and **No pipeline configured** when the target branch has no pipeline file and the project no setting. The activity lists each pipeline the merge request ran, and picking one there shows it on the card. The list of merge requests shows each open one's latest result beside its status, and **Out of date** once its branch has moved past it; open the merge request to see whether its target or jobs have too. None of these is ever the [pipeline a change proposes](#proposed), which has a card of its own. Into a ticked branch, the result decides whether the merge request can merge: see [The merge gate](#merge-gate). Into another protected branch, it is there for you to weigh.

## The merge gate {#merge-gate}

A pipeline guards a merge request when its target is ticked under **Run pipelines** and has a pipeline: a pipeline file on the target's current commit, or the project's setting. A pipeline file that doesn't parse counts too, so a broken file keeps merge requests from merging rather than letting them through. A guarded merge request merges only once its pipeline **passed** on its branch merged onto the target's current commit, with the jobs the target has now, and devpad then merges it only while the target is still at that commit, and only if the merge lands the very tree the pipeline tested, so the tree that passed is the tree that lands, whichever merge strategy you pick. If the target moves as you merge, devpad refuses and the pipeline runs again on the target's new commit.

Until then the merge card's pipeline row says what keeps it from merging: the pipeline is queued or still running, none has run yet, the latest is out of date, it failed, found the branch doesn't merge, doesn't parse, couldn't run or was cancelled. A merge request into a branch you don't tick, or whose target has no pipeline, isn't guarded: the merge card says **No pipeline guards &lt;target&gt;**, and nothing but you, or a review agent's approval for an [automatic merge](/docs/branches#automatic-merges), decides what merges.

**Merge anyway.** You can merge a guarded merge request whose pipeline hasn't passed: **Merge into &lt;target&gt;…** asks you to confirm, naming how the pipeline stands, and **Merge anyway** merges it. What you confirm is that pipeline on those commits: if the pipeline, the branch or the target changes while you decide, even to another pipeline that failed the same way, devpad asks again with how it stands now. A branch that doesn't merge onto its target can't be merged anyway, since the merge would conflict just as the pipeline's did: the merge card lists the conflicting files, and on a task's merge request **Ask the task's agent to resolve them** has the agent merge the target in, after which the pipeline runs again. The merge request then says you merged it although its pipeline hadn't passed, and how it stood, such as **by you although its pipeline failed (#12)**. Only you can do this, from the merge card: agents and the assistant have no way to merge at all, and devpad never merges automatically over the gate.

An [automatic merge](/docs/branches#automatic-merges) needs the pass too, and never takes a change that adds, changes, renames or deletes the pipeline file itself: the target's jobs would test it, but once merged it would decide what every later merge request has to pass, so you merge it yourself. Devpad checks the very files the merge would land, so even a change whose history hides it from a diff can't remove or roll back the pipeline file by itself. When the review agent's approval comes before the pipeline has finished, the merge waits for it, and the merge card says **Merging into &lt;target&gt; once the pipeline passes**; devpad merges once it passes, and records why not when it fails, can't run or is cancelled.

## When a pipeline fails {#failures}

When the pipeline guarding a task's merge request fails, devpad sends the task back to its agent with what failed: for each job that failed, ran out of time or ran out of memory, how it ended and its exit code, its script, devpad's notes on it and the end of its output, up to 6 KB a job and 24 KB in all, marked as the job's output. The whole message stays under 32 KB: jobs that don't fit are named, for the agent to read on the pipeline's page. When the pipeline finds the branch doesn't merge onto the target, the agent gets the conflicting files, as **Ask the task's agent to resolve them** sends them: at most 50, the ones the pipeline's page lists, which says how many more conflict. The agent's new commits move the merge request, and its pipeline runs again.

- **Two rounds in a row.** Devpad sends a task back for its pipeline at most twice in a row, and the task and its merge request say **Sent back after the pipeline failed (1 of 2)**. When it fails again after that, the failure waits for you: the task shows it as the feedback for its next run and says **Waiting for you**, and **Send back to agent** on the task, or **Send back** on the merge request, sends it. **Send back** always takes the pipeline's failure as it stands then along with your note.
- **Stuck.** A round that fails the same jobs with the same output as the failure it was sent back for, leaving aside what changes from run to run such as times, durations, addresses and temporary paths, ends the rounds early: the agent is stuck, and the task says so. So does a round that ends with no new commit, or back at commits the pipeline has already tested, since nothing new can come of it.
- **Starting again.** The count starts again whenever you step in, by sending the task back, editing it or restarting it, when a review requests changes, and whenever the pipeline passes. Editing a task whose failure waits for you, such as to raise its budget, leaves the failure waiting. A conflict counts as a round too.
- **Failing on the target too.** A job that also fails in the target branch's own pipeline of the same commit, with the same jobs, isn't the change's doing: devpad doesn't send it back, it doesn't count, and the merge request's **Pipeline** card marks it **also failing on &lt;target&gt;**. This goes by job: a new failure inside a job that already fails on the target is left to you too. Until the target's pipeline of that commit has finished, or while devpad is still to start it, devpad waits to decide. When every failing job fails on the target too, nothing goes back. The merge gate still holds: fix the target first, or merge anyway knowing why it's red.
- **Not the agent's to fix.** A pipeline that couldn't run, was cancelled or replaced, or doesn't parse never goes back to the agent; the merge card says why it doesn't count.
- **While a review runs.** A failure that comes while a review agent reviews the same commits, such as after the target moved, waits for the review to end, so the changes it requests reach the agent too.
- A task that has spent its budget keeps the failure for you, as it keeps other feedback, and so does one whose agent devpad can't start, such as while the project isn't ready.

Devpad decides on each pipeline once, even when it restarts. Pipelines you run by hand into a branch you don't tick never send a task back. What a job printed goes only to the task's own agent and to you: the project's assistant, asked about the task, is told which jobs failed and how, never their output.

A task's agent knows the jobs before it finishes: its first prompt lists the target branch's jobs, each with its script and services, and asks it to run them in its sandbox. The sandbox has the services too, under the same connection variables. Those runs are the agent's own check; devpad's pipeline is the one that counts.

**Reviews wait for a pass.** Where a pipeline guards a merge request, the project's [automatic review](/docs/branches#reviews), and the next round of an [auto-fixing review](/docs/branches#auto-fixing-reviews), start only once its pipeline has passed, so the review agent reads a change that passes its jobs. Until then the **Pipeline** card says **The automatic review starts once the pipeline passes**. A review you start yourself starts at once. The review agent is told how the pipeline went, each job's name, result and time, but never what a job printed: that came from the code under review, which could use it to talk its reviewer round.

## A change to the pipeline file {#proposed}

A merge request whose change adds, changes, renames or deletes `.devpad/pipeline.yml` (or `.devpad/pipeline.yaml`) is still graded by its target branch's jobs: once it merges, its file decides what every later merge request has to pass, but not before. So that you can see the jobs it proposes work before they take effect, devpad runs those too, on the merge request's **Proposed pipeline** card, which says **from this change — doesn't gate merging**:

- **What runs.** The pipeline file of the branch merged onto the target's current commit, the very commit the merge request's own pipeline tests, and on that same tree. The project's pipeline setting never stands in for it.
- **What it decides.** Nothing. A proposed pipeline never keeps the merge request from merging nor lets it through, never goes back to the task's agent, never holds back or starts a review, never decides an [automatic merge](/docs/branches#automatic-merges), and is never the merge request's latest result: its badge, its merge card, its activity and the list of merge requests show only the merge request's own pipeline. The **Pipelines** surface lists both, the proposed one marked **proposed**.
- **When it runs.** Whenever the merge request's own pipeline would: by itself into a ticked branch, when it opens, when its branch gets new commits and when the target moves, at most once every 30 seconds like the merge request's own; into another branch when you press **Run proposed pipeline** on the card. A newer proposed pipeline replaces an older one, one whose branch moves to commits that no longer propose a pipeline stops, a merge request that closes or merges stops it, and devpad runs it again after a restart, as it does the merge request's own; the two never replace each other. Once the merge request has merged or closed, the card stays beside its **Pipeline** card and shows the latest proposed pipeline that ran, as that card shows the merge request's own latest.
- **What it may cost.** Its jobs are the change's, so they never hold back the pipelines that count: they take a job slot only after every job of those, and only one at a time, and each runs for at most as long as the target's longest job, or 10 minutes when the target has no pipeline, whatever its `timeout` asks. A pipeline whose jobs would run on another commit than the one they were read from ends with an error instead.
- **When nothing runs.** A change that removes the pipeline file proposes no jobs: the card says **This change removes the pipeline**, and once it merges the target's commits run the project's pipeline setting, or no pipeline. A branch that doesn't merge onto its target proposes nothing that can be read. A change that proposes the very jobs the merge request's own pipeline runs, such as by changing only a comment or renaming the file to `.devpad/pipeline.yaml`, runs them once, in that pipeline, and the card says so. A proposed file that doesn't parse shows its error, as an **Invalid** pipeline does.

Devpad never merges a change to the pipeline file by itself, so you merge it once you've seen its jobs at work.

## The look-closely mark {#look-closely}

A passing pipeline says the jobs ran clean, not that they still test what they did. Devpad works out from each merge request's change, the files its branch changed since it left the target, which of them decide what the jobs prove, and marks the merge request when there are any, so you know where to look closely:

- **Deletes tests**: a test file the change deletes or turns into something else, such as a symlink, renames to a name that is no test's, or moves where test runners don't look, such as a `testdata/` directory, one whose name starts with `_` or `.`, or `vendor/`. Test files are those of the common conventions, such as `*_test.go`, `*.test.*`, `*.spec.*` and `*.cy.*`, `test_*.py` and `*_test.py`, `*_spec.rb` and `*Test.java`, and any file in a `test/`, `tests/`, `__tests__/`, `spec/` or `testdata/` directory;
- **Changes the pipeline file**;
- **Changes files the pipeline's jobs run**: a file the target branch's jobs name in their scripts, such as `./scripts/test.sh`, or `frontend/tools/check.mjs` for `cd frontend && node ./tools/check.mjs`. These come from the target's pipeline, never from the change's own, and devpad works the mark out again whenever the target moves;
- **Changes CI configuration**, such as `.github/workflows/`, Gitea or Forgejo workflows, `.gitlab-ci.yml`, CircleCI or a `Jenkinsfile`;
- **Changes lint, type-check or test settings**, such as `tsconfig*.json` and `jsconfig*.json`, ESLint, Prettier, golangci-lint, Ruff, mypy, pytest, Vitest, Jest or Vite configuration and their setup files, `setup.cfg`, `tox.ini` or `.pre-commit-config.yaml`;
- **Changes build scripts, dependencies or toolchains**, such as a `Makefile`, `package.json` and its lockfile, `go.mod` and `go.sum`, `Cargo.toml`, `pyproject.toml`, a requirements file, Bazel's `BUILD` and `MODULE.bazel`, or a version pin such as `.nvmrc`, `.tool-versions` or `rust-toolchain.toml`.

The merge request's Overview lists them on a **Look closely** card, by reason, each opening in **Changes**. It names at most 50 files, those of the reasons first in this list first, so deleted tests always show, and says how many files each reason covers. The list of merge requests shows an eye beside the merge request's status, which says how many files of each reason. The review agent is given the same list in devpad's own words. It is a mark, never a gate: nothing waits on it, and a change can weaken its tests in ways no file name shows, such as by skipping a test inside a file it keeps.

## What pipelines don't stop {#limits}

Reading the jobs from the target branch makes cheating visible, not impossible. A change can still weaken what the jobs check:

- delete a test, or skip one with `t.Skip` or `it.skip`;
- change what a command does, such as a `Makefile` target or a script in `package.json`;
- loosen a type-check or lint configuration.

The review agent is told to look for exactly these: deleted or skipped tests, changed build or test scripts, and loosened lint or type-check settings, which it treats as blocking unless the task asks for them, and is given the files devpad [marked](#look-closely) for a closer look. Still, review a change's tests and build files as closely as its code: the **Look closely** card shows where to start.
