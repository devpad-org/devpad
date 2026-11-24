import type { AgentRun, Usage } from '@/api/ai'
import { isAgentRunActiveStatus } from '@/stores/agentRuns'
import { formatUsd } from '@/utils/cost'

// How the Agent Runs panel arranges a workspace's runs. A conversation is a
// thread: each message sent in it starts one run, and the sub-agents a run
// starts hang under it, so a message and a sub-agent are never drawn alike.

// UsageTotals adds up a run's usage and its sub-agents'. priced is set when
// any of them has a cost and unpriced when any used tokens without one.
export interface UsageTotals {
  inputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheWriteTokens: number
  costUsd: number
  ownCostUsd: number
  subRuns: number
  priced: boolean
  unpriced: boolean
}

// A run and the sub-agents it started, oldest first. usage includes theirs.
export interface RunNode {
  run: AgentRun
  usage: UsageTotals
  children: RunNode[]
}

export type ThreadStatus = 'waiting' | 'running' | 'failed' | 'idle'

// One conversation, or a run outside any conversation.
export interface RunThread {
  key: string
  title: string
  // The messages, oldest first: runs with no parent. A sub-agent whose
  // parent isn't listed sits here too, drawn as the sub-agent it is.
  roots: RunNode[]
  latestActivity: number
  usage: UsageTotals
  messages: number
  subAgents: number
  status: ThreadStatus
  runIds: Set<number>
}

export function isAgentRunWaitingStatus(status: AgentRun['status']): boolean {
  return status === 'waiting_approval' || status === 'waiting_user'
}

export function isSubAgentRun(run: AgentRun): boolean {
  return Boolean(run.parentRunId)
}

export function buildRunThreads(runs: AgentRun[]): RunThread[] {
  const byId = new Map(runs.map((run) => [run.id, run]))
  const children = new Map<number, AgentRun[]>()
  const roots: AgentRun[] = []
  for (const run of runs) {
    if (run.parentRunId && byId.has(run.parentRunId)) {
      const siblings = children.get(run.parentRunId) ?? []
      siblings.push(run)
      children.set(run.parentRunId, siblings)
    } else {
      roots.push(run)
    }
  }

  const byStart = (a: AgentRun, b: AgentRun) => runStartTime(a) - runStartTime(b) || a.id - b.id
  const toNode = (run: AgentRun): RunNode => {
    const kids = (children.get(run.id) ?? []).sort(byStart).map(toNode)
    const usage = addUsage(emptyUsageTotals(), run.usage)
    usage.ownCostUsd = usage.costUsd
    for (const kid of kids) {
      mergeUsage(usage, kid.usage)
      usage.subRuns += 1 + kid.usage.subRuns
    }
    return { run, usage, children: kids }
  }

  const threads = new Map<string, RunThread>()
  for (const run of roots.sort(byStart)) {
    const key = run.conversationId ? `c${run.conversationId}` : `r${run.id}`
    let thread = threads.get(key)
    if (!thread) {
      thread = {
        key,
        title: '',
        roots: [],
        latestActivity: 0,
        usage: emptyUsageTotals(),
        messages: 0,
        subAgents: 0,
        status: 'idle',
        runIds: new Set(),
      }
      threads.set(key, thread)
    }
    const node = toNode(run)
    thread.roots.push(node)
    mergeUsage(thread.usage, node.usage)
  }

  for (const thread of threads.values()) {
    let active = false
    let waiting = false
    const visit = (node: RunNode) => {
      const { run } = node
      thread.runIds.add(run.id)
      if (isSubAgentRun(run)) thread.subAgents++
      else thread.messages++
      thread.latestActivity = Math.max(thread.latestActivity, runActivityTime(run))
      active ||= isAgentRunActiveStatus(run.status)
      waiting ||= isAgentRunWaitingStatus(run.status)
      node.children.forEach(visit)
    }
    thread.roots.forEach(visit)

    const first = thread.roots.find((node) => !isSubAgentRun(node.run)) ?? thread.roots[0]
    thread.title = runTitle(first.run)
    const last = thread.roots[thread.roots.length - 1].run
    thread.status = waiting ? 'waiting' : active ? 'running' : last.status === 'failed' ? 'failed' : 'idle'
  }

  // The most recently active conversation comes first.
  return [...threads.values()].sort((a, b) => b.latestActivity - a.latestActivity || b.key.localeCompare(a.key))
}

export function runTitle(run: AgentRun): string {
  return run.promptPreview?.trim() || `Run #${run.id}`
}

function emptyUsageTotals(): UsageTotals {
  return {
    inputTokens: 0,
    outputTokens: 0,
    cacheReadTokens: 0,
    cacheWriteTokens: 0,
    costUsd: 0,
    ownCostUsd: 0,
    subRuns: 0,
    priced: false,
    unpriced: false,
  }
}

function addUsage(totals: UsageTotals, usage: Usage | undefined): UsageTotals {
  if (!usage) return totals
  totals.inputTokens += usage.inputTokens
  totals.outputTokens += usage.outputTokens
  totals.cacheReadTokens += usage.cacheReadTokens ?? 0
  totals.cacheWriteTokens += usage.cacheWriteTokens ?? 0
  if (usage.costUsd !== undefined) {
    totals.costUsd += usage.costUsd
    totals.priced = true
  } else if (usage.inputTokens + usage.outputTokens > 0) {
    totals.unpriced = true
  }
  return totals
}

function mergeUsage(totals: UsageTotals, other: UsageTotals): void {
  totals.inputTokens += other.inputTokens
  totals.outputTokens += other.outputTokens
  totals.cacheReadTokens += other.cacheReadTokens
  totals.cacheWriteTokens += other.cacheWriteTokens
  totals.costUsd += other.costUsd
  totals.priced ||= other.priced
  totals.unpriced ||= other.unpriced
}

export function usageTokenCount(usage: UsageTotals): number {
  return usage.inputTokens + usage.outputTokens + usage.cacheReadTokens + usage.cacheWriteTokens
}

const compactNumber = new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 })

export function formatUsage(usage: UsageTotals): string {
  if (usage.priced) return usage.unpriced ? `${formatUsd(usage.costUsd)}+` : formatUsd(usage.costUsd)
  return `${compactNumber.format(usageTokenCount(usage))} tokens`
}

export function formatUsageTitle(usage: UsageTotals): string {
  const lines: string[] = []
  if (usage.priced) {
    lines.push(`Cost: $${usage.costUsd.toFixed(4)}`)
  }
  if (usage.subRuns > 0) {
    const plural = usage.subRuns === 1 ? 'sub-agent' : 'sub-agents'
    lines.push(usage.priced
      ? `Includes ${usage.subRuns} ${plural} ($${(usage.costUsd - usage.ownCostUsd).toFixed(4)})`
      : `Includes ${usage.subRuns} ${plural}`)
  }
  if (usage.unpriced) {
    lines.push(usage.priced ? 'Some tokens are from models without known prices and are not in the cost.' : 'This model has no known prices.')
  }
  const tokens = [
    `Input ${usage.inputTokens.toLocaleString()}`,
    `Output ${usage.outputTokens.toLocaleString()}`,
  ]
  if (usage.cacheReadTokens > 0) tokens.push(`Cache read ${usage.cacheReadTokens.toLocaleString()}`)
  if (usage.cacheWriteTokens > 0) tokens.push(`Cache write ${usage.cacheWriteTokens.toLocaleString()}`)
  lines.push(tokens.join(' · '))
  return lines.join('\n')
}

// A message's run is the assistant replying; a sub-agent's is a piece of work
// it was handed, so a finished one is "Done" rather than "Replied".
const statusLabels: Record<AgentRun['status'], string> = {
  queued: 'Queued',
  running: 'Running',
  waiting_approval: 'Needs approval',
  waiting_user: 'Needs input',
  completed: 'Replied',
  failed: 'Failed',
  cancelled: 'Cancelled',
}

export function formatRunStatus(run: AgentRun): string {
  const label = run.status === 'completed' && isSubAgentRun(run) ? 'Done' : statusLabels[run.status] ?? run.status
  const took = runDuration(run)
  if (took === null) return label
  const duration = formatDuration(took)
  if (run.status === 'completed') return `${label} in ${duration}`
  if (run.status === 'failed' || run.status === 'cancelled') return `${label} after ${duration}`
  return label
}

// How long a finished run took, or null while it runs or when unknown.
function runDuration(run: AgentRun): number | null {
  if (isAgentRunActiveStatus(run.status)) return null
  const start = new Date(run.startedAt ?? run.createdAt).getTime()
  const end = new Date(run.completedAt ?? run.updatedAt).getTime()
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return null
  return end - start
}

export function formatDuration(ms: number): string {
  const seconds = Math.max(1, Math.round(ms / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 10) {
    const rest = seconds % 60
    return rest ? `${minutes}m ${rest}s` : `${minutes}m`
  }
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  return rest ? `${hours}h ${rest}m` : `${hours}h`
}

export function formatRelativeTime(dateStr: string | number, now: number): string {
  const date = new Date(dateStr)
  const diffMin = Math.floor((now - date.getTime()) / 60000)
  if (diffMin < 1) return 'just now'
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h ago`
  const diffDay = Math.floor(diffHr / 24)
  if (diffDay < 7) return `${diffDay}d ago`
  return date.toLocaleDateString()
}

export function formatAbsoluteTime(dateStr: string | number): string {
  const date = new Date(dateStr)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : ''
}

export function plural(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`
}

function runActivityTime(run: AgentRun): number {
  const updated = new Date(run.updatedAt).getTime()
  if (Number.isFinite(updated)) return updated
  return runStartTime(run)
}

function runStartTime(run: AgentRun): number {
  const created = new Date(run.createdAt).getTime()
  return Number.isFinite(created) ? created : 0
}
