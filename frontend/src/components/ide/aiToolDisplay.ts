import type { PlanStep } from '@/api/ai'

export interface ToolDisplaySegment {
  type: 'tool'
  toolCallId: string
  name: string
  args: string
  result?: string
}

export interface ToolGroupDisplay {
  type: 'tool-group'
  key: string
  tools: ToolDisplaySegment[]
}

export function parseToolArgs(args: string): Record<string, unknown> | null {
  try {
    const parsed = JSON.parse(args)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>
    }
  } catch {
    // Invalid model-provided JSON is displayed as raw text by callers.
  }

  return null
}

export function formatToolArgs(name: string, args: string): string {
  const parsed = parseToolArgs(args)
  if (parsed) {
    if (name === 'update_plan' && Array.isArray(parsed.steps)) {
      const steps = parsed.steps as { title: string; status: string }[]
      return `${steps.length} steps`
    }

    return Object.entries(parsed)
      .map(([k, v]) => {
        if (typeof v === 'string') return `${k}: ${v.length > 80 ? v.slice(0, 80) + '…' : v}`
        if (Array.isArray(v)) return `${k}: [${v.length} items]`
        if (v && typeof v === 'object') return `${k}: {…}`
        return `${k}: ${v}`
      })
      .join(', ')
  }

  return args
}

export function isToolError(seg: ToolDisplaySegment): boolean {
  if (!seg.result) return false
  return seg.result.startsWith('Error:') || seg.result.startsWith('Command failed') || seg.result.startsWith('Command timed out')
}

export function shouldRenderToolCall(name: string): boolean {
  return name !== 'update_plan'
}

export function planStepsFromToolArgs(args: string): PlanStep[] {
  const parsed = parseToolArgs(args)
  const steps = parsed?.steps
  if (!Array.isArray(steps)) return []

  return steps.flatMap((step): PlanStep[] => {
    if (!step || typeof step !== 'object' || Array.isArray(step)) return []
    const record = step as Record<string, unknown>
    const title = record.title
    const status = record.status
    if (
      typeof title !== 'string' ||
      (status !== 'pending' && status !== 'in_progress' && status !== 'completed' && status !== 'failed')
    ) {
      return []
    }
    return [{ title, status }]
  })
}

export function toolGroupKey(name: string): string {
  if (name === 'read_file' || name === 'read_file_lines') return 'read_files'
  if (name === 'write_file') return 'write_files'
  if (name === 'edit_file') return 'edit_files'
  if (name === 'delete_file') return 'delete_files'
  if (name === 'list_files') return 'list_files'
  if (name === 'search_files') return 'search_files'
  if (name === 'run_command') return 'run_commands'
  if (name === 'spawn_sub_agent') return 'spawn_sub_agents'
  if (name === 'wait_for_sub_agents') return 'wait_for_sub_agents'
  return name
}

export function toolFriendlyName(name: string): string {
  switch (name) {
    case 'read_file':
      return 'Read file'
    case 'read_file_lines':
      return 'Read lines'
    case 'write_file':
      return 'Write file'
    case 'edit_file':
      return 'Edit file'
    case 'delete_file':
      return 'Delete file'
    case 'list_files':
      return 'List folder'
    case 'search_files':
      return 'Search files'
    case 'run_command':
      return 'Run command'
    case 'spawn_sub_agent':
      return 'Start sub-agent'
    case 'wait_for_sub_agents':
      return 'Wait for sub-agents'
    default:
      return name.replace(/_/g, ' ')
  }
}

export function toolTargetLabel(seg: ToolDisplaySegment): string {
  switch (seg.name) {
    case 'read_file':
    case 'write_file':
    case 'edit_file':
    case 'delete_file':
      return truncateMiddle(stringArg(seg.args, 'path') || formatToolArgs(seg.name, seg.args))
    case 'read_file_lines': {
      const path = stringArg(seg.args, 'path')
      const startLine = numberArg(seg.args, 'start_line')
      const endLine = numberArg(seg.args, 'end_line')
      const range = startLine !== null && endLine !== null ? `:${startLine}-${endLine}` : ''
      return truncateMiddle(`${path || formatToolArgs(seg.name, seg.args)}${range}`)
    }
    case 'list_files': {
      const path = stringArg(seg.args, 'path')
      return path ? truncateMiddle(path) : 'Project root'
    }
    case 'search_files': {
      const pattern = stringArg(seg.args, 'pattern')
      const pathFilter = stringArg(seg.args, 'path_filter')
      const target = pathFilter ? `${pattern} in ${pathFilter}` : pattern
      return truncateEnd(target || formatToolArgs(seg.name, seg.args))
    }
    case 'run_command':
      return truncateEnd(stringArg(seg.args, 'command') || formatToolArgs(seg.name, seg.args), 110)
    case 'spawn_sub_agent':
      return truncateEnd(stringArg(seg.args, 'prompt') || formatToolArgs(seg.name, seg.args), 110)
    case 'wait_for_sub_agents': {
      const parsed = parseToolArgs(seg.args)
      const runIds = parsed?.run_ids
      if (Array.isArray(runIds) && runIds.length > 0) return `Runs ${runIds.join(', ')}`
      return formatToolArgs(seg.name, seg.args)
    }
    default:
      return truncateEnd(formatToolArgs(seg.name, seg.args))
  }
}

export function toolGroupTitle(group: ToolGroupDisplay): string {
  const first = group.tools[0]
  const count = group.tools.length
  const target = first ? toolTargetLabel(first) : ''

  switch (toolGroupKey(first?.name ?? '')) {
    case 'read_files':
      return count === 1 ? `Read ${target}` : `Read ${count} ${pluralize(count, 'file')}`
    case 'write_files':
      return count === 1 ? `Write ${target}` : `Write ${count} ${pluralize(count, 'file')}`
    case 'edit_files':
      return count === 1 ? `Edit ${target}` : `Edit ${count} ${pluralize(count, 'file')}`
    case 'delete_files':
      return count === 1 ? `Delete ${target}` : `Delete ${count} ${pluralize(count, 'item')}`
    case 'list_files':
      return count === 1 ? `List ${target}` : `List ${count} folders`
    case 'search_files':
      return count === 1 ? `Search ${target}` : `Search ${count} patterns`
    case 'run_commands':
      return count === 1 ? 'Run command' : `Run ${count} commands`
    case 'spawn_sub_agents':
      return count === 1 ? 'Start sub-agent' : `Start ${count} sub-agents`
    case 'wait_for_sub_agents':
      return count === 1 ? 'Wait for sub-agents' : `Wait ${count} times for sub-agents`
    default:
      return count === 1 ? toolFriendlyName(first?.name ?? 'Tool call') : `${count} tool calls`
  }
}

export function toolGroupSubtitle(group: ToolGroupDisplay): string {
  if (group.tools.length === 1) return toolResultMeta(group.tools[0])
  const failed = group.tools.filter(isToolError).length
  const running = group.tools.filter((tool) => !tool.result).length
  if (running > 0) return `${running} running`
  if (failed > 0) return `${failed} failed`
  return 'Completed'
}

export function toolGroupState(group: ToolGroupDisplay): 'running' | 'error' | 'done' {
  if (group.tools.some((tool) => !tool.result)) return 'running'
  if (group.tools.some(isToolError)) return 'error'
  return 'done'
}

export function toolStatusLabel(seg: ToolDisplaySegment): string {
  if (!seg.result) return 'Running'
  return isToolError(seg) ? 'Issue' : 'Done'
}

export function toolResultMeta(seg: ToolDisplaySegment): string {
  if (!seg.result) return 'Running'
  if (isToolError(seg)) return 'Needs attention'

  switch (seg.name) {
    case 'read_file':
    case 'read_file_lines': {
      const lines = seg.result === '' ? 0 : seg.result.split('\n').length
      return `${lines} ${pluralize(lines, 'line')}`
    }
    case 'search_files':
    case 'list_files': {
      try {
        const parsed = JSON.parse(seg.result)
        if (Array.isArray(parsed)) {
          return `${parsed.length} ${seg.name === 'search_files' ? pluralize(parsed.length, 'match', 'matches') : pluralize(parsed.length, 'item')}`
        }
      } catch {
        return 'Completed'
      }
      return 'Completed'
    }
    case 'run_command': {
      const lines = seg.result.trim() === '' ? 0 : seg.result.trim().split('\n').length
      return lines === 0 ? 'No output' : `${lines} output ${pluralize(lines, 'line')}`
    }
    case 'spawn_sub_agent':
    case 'wait_for_sub_agents': {
      try {
        const parsed = JSON.parse(seg.result)
        if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
          const message = (parsed as Record<string, unknown>).message
          if (typeof message === 'string') return message
        }
      } catch {
        return 'Completed'
      }
      return 'Completed'
    }
    default:
      return 'Completed'
  }
}

function stringArg(args: string, key: string): string {
  const parsed = parseToolArgs(args)
  const value = parsed?.[key]
  return typeof value === 'string' ? value : ''
}

function numberArg(args: string, key: string): number | null {
  const parsed = parseToolArgs(args)
  const value = parsed?.[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function truncateMiddle(value: string, maxLength = 82): string {
  if (value.length <= maxLength) return value
  const half = Math.floor((maxLength - 1) / 2)
  return `${value.slice(0, half)}…${value.slice(value.length - half)}`
}

function truncateEnd(value: string, maxLength = 96): string {
  if (value.length <= maxLength) return value
  return `${value.slice(0, maxLength - 1)}…`
}

function pluralize(count: number, singular: string, plural = `${singular}s`): string {
  return count === 1 ? singular : plural
}
