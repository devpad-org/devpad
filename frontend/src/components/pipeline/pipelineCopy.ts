import type { PipelineRounds } from '@/api/tasks'

// What the app says of when devpad starts pipelines by itself, of the
// merge gate and of the pipeline a change proposes, in one place. The docs
// say the same in docs/09-pipelines.md (the introduction, #automatic-runs,
// #branches, #merge-requests, #merge-gate, #failures and #proposed),
// docs/04-branches.md (the Run pipelines ticks, #reviews,
// #auto-fixing-reviews, #merging and #automatic-merges), docs/05-tasks.md
// (#how-a-task-runs) and docs/03-projects.md (the Pipelines surface and the
// pipeline setting).
export const PIPELINE_COPY = {
  // Beside where pipelines are run.
  automaticRuns: 'Devpad runs ticked branches and the merge requests into them by itself. Run any protected branch here, or a merge request from its page.',
  // Beside the Run pipelines ticks.
  tickedBranches:
    "Devpad runs a ticked branch's pipeline on each new commit, and each merge request's into it as it opens and moves. A merge request into a ticked branch merges only once its pipeline passes, unless you merge it anyway.",
  // On a merge request into a ticked branch without a pipeline.
  mergeRequestNoneAutomatic: (target: string) =>
    `No pipeline has run on this merge request yet. Devpad runs one by itself when it opens, when its branch gets new commits and when ${target} moves.`,
  // On a merge request into a branch not ticked to run pipelines.
  mergeRequestNone: (target: string) =>
    `No pipeline has run on this merge request yet. ${target} isn't ticked under Run pipelines, so devpad doesn't run its merge requests by itself: run one here.`,
  // On the Pipeline setting card, with the branches ticked, or none.
  settingTicked: (branches: string) =>
    `Devpad runs ${branches} and the merge requests into them by itself, and runs them again when this setting changes the jobs they get. Other protected branches, and merge requests into them, run by hand.`,
  settingUnticked:
    'Pipelines run by hand on any protected branch and on merge requests into one. Tick branches under Run pipelines in Manage branches for devpad to run them, and the merge requests into them, by itself.',
  // What a merge request's pipeline decides: whether the merge gate guards
  // it, and when it doesn't, why not.
  gateGuarded: (target: string) =>
    `${target} is ticked under Run pipelines, so this merges only once its pipeline passes on this branch merged onto ${target} as it is then, unless you merge it anyway.`,
  gateNotTicked: (target: string) =>
    `No pipeline guards ${target}: it isn't ticked under Run pipelines, so a result here doesn't keep this from merging.`,
  gateNotConfigured: (target: string) => `No pipeline guards ${target}: it has no .devpad/pipeline.yml and the project no pipeline setting.`,
  gateShut: 'Merging waits for it to pass, unless you merge anyway.',
  gateConflict: "It can't merge until the conflicts are resolved; its pipeline runs again then.",
  // Asking to merge over a shut gate, and what the merge request keeps.
  overrideTitle: 'Merge without a passing pipeline?',
  overrideChanged: 'The pipeline or the commits changed. Merge anyway?',
  overrideRecorded: 'The merge request records that you merged it anyway, and how its pipeline stood.',
  // A task's merge request into a branch that merges automatically.
  autoMergeGuarded: (reviewer: string, target: string) => `Once ${reviewer} approves it and its pipeline passes on ${target} as it is then.`,
  autoMergeUnguarded: (reviewer: string, target: string) =>
    `Once ${reviewer} approves it. No pipeline guards ${target}, so nothing but the review agent's judgment decides what merges.`,
  autoMergeWaiting: (target: string) => `The review agent approved it; devpad merges it into ${target} once its pipeline passes.`,
  autoMergePipelineFile: (file: string) => `It changes ${file}, which devpad never merges by itself: merge it yourself once you've looked at the change to its jobs.`,
  // In the Branches dialog, under Merge automatically.
  autoMergeUnguardedWarning: (branches: string) =>
    `No pipeline guards ${branches}, so nothing but the review agent's judgment decides what merges. A change can carry text that tries to talk the review agent into approving it. Tick a branch under Run pipelines, with a pipeline configured, for its merge requests to pass the pipeline first.`,
  autoMergeGuardedHint: (branches: string) =>
    `A pipeline guards ${branches}: a task's merge request merges only once its pipeline passes too. A pipeline can't tell a weakened test from a sound one, so the review agent's judgment still counts.`,
  // Beside a failing job that fails on the target too.
  alsoFailing: (target: string) => `also failing on ${target}`,
  alsoFailingHint: (target: string) => `This job fails in ${target}'s own pipeline of the same commit too, so it isn't sent back to the task's agent.`,
  targetPending: (target: string) => `Waiting for ${target}'s own pipeline of the same commit, to tell the failures this change brings from those already there.`,
  // The project's automatic review of a merge request a pipeline guards.
  reviewAwaitsPipeline: 'The automatic review starts once the pipeline passes.',
  // The pipeline a merge request's change proposes, on its own card.
  proposedNote: "from this change — doesn't gate merging",
  proposedExplain: (file: string, target: string) =>
    `This change touches ${file}, so devpad also runs the jobs it proposes on this branch merged onto ${target}, for you to see they work before they take effect. The result never keeps this from merging, never goes back to the task's agent and never holds a review: until it merges, ${target}'s own jobs are the ones that count. Its jobs run one at a time, after any job of the pipelines that count, each for at most as long as ${target}'s longest.`,
  proposedRemoved: (target: string) =>
    `This change removes the pipeline, so it proposes no jobs and nothing runs here. Once it merges, ${target}'s commits run the project's pipeline setting, or no pipeline when it has none.`,
  proposedConflict: (target: string) => `This branch doesn't merge onto ${target}, so the pipeline it proposes can't be read.`,
  proposedSameJobs: (target: string) =>
    `This change proposes the very jobs ${target}'s pipeline already runs, such as by changing only a comment, so they run once, on the Pipeline card above.`,
  proposedNoneAutomatic: "No proposed pipeline has run yet. Devpad runs it by itself whenever it runs this merge request's own.",
  proposedNone: (target: string) => `No proposed pipeline has run yet. ${target} isn't ticked under Run pipelines, so run it here.`,
} as const

// What a task's automatic pipeline rounds say, on the task and on its merge
// request, or '' when there is nothing to say: the task was sent back
// because its pipeline failed, or the failure waits for you.
export function pipelineRoundsText(rounds: PipelineRounds | undefined): string {
  if (!rounds) return ''
  switch (rounds.held) {
    case 'limit':
      return `Waiting for you: the pipeline still failed after ${rounds.limit} automatic rounds. Send the failure back, or look at the change.`
    case 'stuck':
      return "Waiting for you: stuck, the agent's last round left the same failures as before. Send the failure back with a hint, or look at the change."
    case 'not_started':
      return "Waiting for you: devpad couldn't start the agent to send the pipeline's failure back. Send it back once the project is ready."
    case 'budget':
      return ''
  }
  return rounds.count > 0 ? `Sent back after the pipeline failed (${rounds.count} of ${rounds.limit}).` : ''
}
