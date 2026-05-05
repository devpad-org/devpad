import { ref } from 'vue'
import {
  gitApi,
  gitSshHostKeyFromError,
  type GitActionResult,
  type GitSshHostKey,
} from '@/api/git'

type RetryFn = () => Promise<void>

export function useGitSshHostKey(workspaceId: () => number) {
  const hostKey = ref<GitSshHostKey | null>(null)
  const accepting = ref(false)
  const error = ref('')
  let retry: RetryFn | null = null

  function promptForHostKey(nextHostKey: GitSshHostKey, retryFn: RetryFn): true {
    hostKey.value = nextHostKey
    retry = retryFn
    error.value = ''
    return true
  }

  function promptFromActionResult(result: GitActionResult, retryFn: RetryFn): boolean {
    if (!result.sshHostKey) return false
    return promptForHostKey(result.sshHostKey, retryFn)
  }

  function promptFromError(err: unknown, retryFn: RetryFn): boolean {
    const nextHostKey = gitSshHostKeyFromError(err)
    if (!nextHostKey) return false
    return promptForHostKey(nextHostKey, retryFn)
  }

  function cancel() {
    hostKey.value = null
    accepting.value = false
    error.value = ''
    retry = null
  }

  async function accept() {
    const currentHostKey = hostKey.value
    if (!currentHostKey || accepting.value) return

    accepting.value = true
    error.value = ''
    try {
      const result = await gitApi.acceptSshHostKey(workspaceId(), currentHostKey)
      if (!result.success) {
        error.value = result.error || result.output || 'Failed to accept SSH host key'
        return
      }

      const retryFn = retry
      cancel()
      if (retryFn) await retryFn()
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to accept SSH host key'
    } finally {
      accepting.value = false
    }
  }

  return {
    hostKey,
    accepting,
    error,
    promptFromActionResult,
    promptFromError,
    accept,
    cancel,
  }
}
