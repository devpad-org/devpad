// When this browser last opened each project, so the launcher can put the
// projects being worked on first. Storage may be unavailable (private
// windows, blocked site data); everything here degrades to "never opened".

const STORAGE_KEY = 'devpad.projectsOpenedAt'

function read(): Record<string, string> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    const parsed: unknown = raw ? JSON.parse(raw) : {}
    return parsed && typeof parsed === 'object' ? (parsed as Record<string, string>) : {}
  } catch {
    return {}
  }
}

export function recordProjectOpened(projectId: number): void {
  try {
    const opened = read()
    opened[String(projectId)] = new Date().toISOString()
    localStorage.setItem(STORAGE_KEY, JSON.stringify(opened))
  } catch {
    // best effort
  }
}

export function projectsOpenedAt(): Map<number, string> {
  return new Map(Object.entries(read()).map(([id, at]) => [Number(id), at]))
}
