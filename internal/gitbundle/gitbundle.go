// Package gitbundle holds what devpad and the workspace agent agree on
// about the git bundles that move commits between a project's canonical
// repo, its workspaces and its pipelines' jobs.
package gitbundle

// MaxBytes caps a bundle: the agent's cap on those it accepts, and the
// host's on those no agent receives, such as a pipeline's.
const MaxBytes = 512 << 20
