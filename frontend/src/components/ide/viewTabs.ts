import { inject, type InjectionKey, type Ref } from 'vue'

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

// The merge request a view tab shows, or null for other views.
export function mergeRequestTabId(id: string): number | null {
  const match = /^mr:([1-9]\d*)$/.exec(id)
  return match ? Number(match[1]) : null
}

// Reads a view id from untrusted input such as the URL query.
export function parseViewTabId(value: unknown): ViewTabId | null {
  if (value === BOARD_TAB) return BOARD_TAB
  if (typeof value !== 'string') return null
  const mr = mergeRequestTabId(value)
  return mr === null ? null : mergeRequestTab(mr)
}

// Asks the user before an action throws away unsaved editor changes. It
// resolves true when there are none, or when the user agrees to lose them.
export const confirmDiscardKey: InjectionKey<(message: string) => Promise<boolean>> = Symbol('confirmDiscard')

// What the project shell offers the panels and tabs inside it.
export interface IdeShell {
  // Opens a view tab, or focuses it if it is already open.
  openView(id: ViewTabId): void
  // Opens a workspace file in the editor.
  openFile(path: string): void
  // The view tab showing, if any.
  activeView: Readonly<Ref<ViewTabId | null>>
  // The project workspace, while it is running.
  liveWorkspaceId: Readonly<Ref<number | null>>
}

export const ideShellKey: InjectionKey<IdeShell> = Symbol('ideShell')

// Opens a merge request as a tab in the project shell. Every caller sits
// inside the shell.
export function useOpenMergeRequest(): (mrId: number) => void {
  const shell = inject(ideShellKey, null)
  return (mrId) => shell?.openView(mergeRequestTab(mrId))
}
