// Turns a refused branch operation on the project workspace into a message
// that says what to do next.
export function branchActionError(e: unknown, fallback: string): string {
  const message = e instanceof Error && e.message ? e.message : fallback
  if (/would be overwritten|commit your changes or stash/i.test(message)) {
    return 'You have uncommitted changes that this would overwrite. Commit or stash them first.'
  }
  if (/has commits the published branch lacks/i.test(message)) {
    return `The project's copy of this branch has commits yours doesn't. ${message.split(';').pop()?.trim() ?? ''}`.trim()
  }
  if (/branch already belongs to a sandbox/i.test(message)) {
    return 'An agent sandbox owns this branch. Wait for it to finish, or check the branch out and publish it under a new name.'
  }
  if (/being published/i.test(message)) {
    return 'This branch is being published right now. Try again in a moment.'
  }
  if (/branch not found/i.test(message)) {
    return "The project doesn't have this branch yet. An agent's branch appears once it publishes its work."
  }
  if (/workspace is not running|not running/i.test(message)) {
    return 'Start the workspace first.'
  }
  return message
}
