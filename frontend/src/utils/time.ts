// Compact relative time, e.g. "5m ago"; older than a month shows the date.
export function timeAgo(iso: string | null | undefined): string {
  if (!iso) return ''
  const seconds = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000)
  if (seconds < 60) return 'just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days}d ago`
  return new Date(iso).toLocaleDateString()
}

// Full local timestamp, for tooltips next to timeAgo.
export function fullTime(iso: string | null | undefined): string {
  return iso ? new Date(iso).toLocaleString() : ''
}

// A Unix timestamp in seconds (as git's %at gives a commit's date) as ISO,
// for timeAgo and fullTime; null when it isn't a valid date.
export function unixSecondsToIso(seconds: string | number | null | undefined): string | null {
  if (seconds === null || seconds === undefined || seconds === '') return null
  const date = new Date(Number(seconds) * 1000)
  return Number.isNaN(date.getTime()) ? null : date.toISOString()
}
