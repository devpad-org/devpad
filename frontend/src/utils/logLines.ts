import { PLAIN, parseLine, type AnsiSegment, type AnsiStyle } from './ansi'

// How many lines a log keeps, dropping the oldest past it, and how much of
// one line it shows: a job's output is the last 1 MB of what it printed,
// so these bound what the page holds whatever the job printed.
export const MAX_LOG_LINES = 50_000
export const MAX_LINE_CHARS = 2_000

export interface LogLine {
  // Unique in its log, for keyed rendering.
  id: number
  segments: AnsiSegment[]
  // The line was longer than MAX_LINE_CHARS and is cut there.
  cut: boolean
}

// LogLines keeps the lines of output that arrives in chunks, which may
// end part way through a line or an escape code: the last, open line is
// parsed again with each chunk that adds to it.
export class LogLines {
  lines: LogLine[] = []
  // Lines dropped from the start to stay under MAX_LOG_LINES.
  droppedLines = 0
  private open = ''
  private openStyle: AnsiStyle = { ...PLAIN }
  // The id of the open line as last shown, or -1 when there is none.
  private openId = -1
  private nextId = 0

  append(data: string) {
    if (!data) return
    const pieces = (this.open + data).split('\n')
    this.open = pieces.pop() ?? ''
    // The open line was shown as it stood; it is complete now, or longer.
    if (this.lines.length > 0 && this.lines[this.lines.length - 1].id === this.openId) this.lines.pop()
    for (const piece of pieces) {
      const parsed = parseLine(piece, this.openStyle)
      this.openStyle = parsed.style
      this.lines.push(this.line(parsed.segments, this.nextId++))
    }
    if (this.open) {
      this.openId = this.nextId
      this.lines.push(this.line(parseLine(this.open, this.openStyle).segments, this.openId))
    } else {
      this.openId = -1
    }
    const over = this.lines.length - MAX_LOG_LINES
    if (over > 0) {
      this.lines.splice(0, over)
      this.droppedLines += over
    }
  }

  clear() {
    this.lines = []
    this.droppedLines = 0
    this.open = ''
    this.openStyle = { ...PLAIN }
    this.openId = -1
  }

  private line(segments: AnsiSegment[], id: number): LogLine {
    let left = MAX_LINE_CHARS
    let cut = false
    const kept: AnsiSegment[] = []
    for (const s of segments) {
      if (s.text.length > left) {
        if (left > 0) kept.push({ ...s, text: s.text.slice(0, left) })
        cut = true
        break
      }
      kept.push(s)
      left -= s.text.length
    }
    return { id, segments: kept, cut }
  }
}
