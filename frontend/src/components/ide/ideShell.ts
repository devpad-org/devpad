import type { InjectionKey, Ref } from 'vue'

// Asks the user before an action throws away unsaved editor changes. It
// resolves true when there are none, or when the user agrees to lose them.
export const confirmDiscardKey: InjectionKey<(message: string) => Promise<boolean>> = Symbol('confirmDiscard')

// How the project workspace's git state moved. A checkout changes the
// files; a commit or a new branch ("head") moves HEAD but leaves them as they
// are; publish and sync move only refs.
export type BranchChange = 'checkout' | 'head' | 'publish' | 'sync'

// What the project shell offers the panels and modals inside it.
export interface IdeShell {
  // Opens a workspace file in the editor, closing a merge request in front
  // of it first.
  openFile(path: string): void
  // The project workspace, while it is running.
  liveWorkspaceId: Readonly<Ref<number | null>>
  // Whether open files have unsaved changes.
  unsavedFiles: Readonly<Ref<boolean>>
  // Saves every open file, resolving true once none is left unsaved.
  saveAllFiles(): Promise<boolean>
}

export const ideShellKey: InjectionKey<IdeShell> = Symbol('ideShell')
