import type { InjectionKey } from 'vue'

// A view tab is an editor tab the IDE shell renders itself, such as the
// project board or a merge request. View ids never start with "/", so they
// can't collide with file tabs, whose ids are absolute workspace paths.
export type ViewTabId = 'board' | `mr:${number}`

export interface ViewTab {
  id: ViewTabId
  name: string
}

export const BOARD_TAB: ViewTabId = 'board'

export function mergeRequestTab(id: number): ViewTabId {
  return `mr:${id}`
}

// Reads a view id from untrusted input such as the URL query.
export function parseViewTabId(value: unknown): ViewTabId | null {
  if (value === BOARD_TAB) return BOARD_TAB
  if (typeof value !== 'string') return null
  const match = /^mr:([1-9]\d*)$/.exec(value)
  return match ? mergeRequestTab(Number(match[1])) : null
}

// Asks the user before an action throws away unsaved editor changes. It
// resolves true when there are none, or when the user agrees to lose them.
export const confirmDiscardKey: InjectionKey<(message: string) => Promise<boolean>> = Symbol('confirmDiscard')
