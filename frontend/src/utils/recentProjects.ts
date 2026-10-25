// When this browser last opened each project, so the launcher can put the
// projects being worked on first. Storage may be unavailable (private
// windows, blocked site data); everything here degrades to "never opened".

const STORAGE_KEY = 'devpad.projectsOpenedAt'

// Only entries with a numeric id and a parseable time survive, so a stored
// value from an older version or a hand edit can't break the launcher.
function read(): Record<string, string> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    const parsed: unknown = raw ? JSON.parse(raw) : {}
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    const valid: Record<string, string> = {}
    for (const [id, at] of Object.entries(parsed)) {
      if (Number.isInteger(Number(id)) && typeof at === 'string' && Number.isFinite(Date.parse(at))) valid[id] = at
    }
    return valid
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
