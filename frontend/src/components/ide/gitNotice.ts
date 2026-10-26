// A message the Source Control panel shows after a git action: what
// happened, and for a failure the output git printed.
export interface GitNotice {
  tone: 'success' | 'error' | 'info'
  text: string
  detail?: string
}
