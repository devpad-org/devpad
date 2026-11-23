import { PLAIN, advanceStyle, parseLine, type AnsiSegment, type AnsiStyle } from './ansi'

// How many lines a log keeps, dropping the oldest past it, and how much of
// one line it shows. Together they bound what the page holds, whatever a job
// prints and for however long it is followed.
export const MAX_LOG_LINES = 50_000
export const MAX_LINE_CHARS = 2_000
// How much of the line still open is kept: what is shown, with room for the
// escape codes in it. The rest of a longer line is dropped as it comes.
const MAX_OPEN_CHARS = MAX_LINE_CHARS + 2_000

export interface LogLine {
  // Unique in its log, for keyed rendering.
  id: number
  segments: AnsiSegment[]
  // The line was longer than MAX_LINE_CHARS and is cut there.
  cut: boolean
  // Not a line of output: it marks where output was lost.
  gap?: boolean
}

// LogLines keeps the lines of output that arrives in chunks, which may end
// part way through a line or an escape code. The last line, not yet ended
// by a newline, is held apart: only what follows its last carriage return
// (a progress bar redraws the line with one), and no more than
// MAX_OPEN_CHARS of that, so a job printing without a newline costs the same
// for each chunk however long it goes on.
export class LogLines {
  lines: LogLine[] = []
  // Lines dropped from the start to stay under MAX_LOG_LINES.
  droppedLines = 0
  // The open line's text, and the style it starts in.
  private open = ''
  private openStyle: AnsiStyle = { ...PLAIN }
  // The open line was longer than MAX_OPEN_CHARS; cutStyle is the style
  // what was dropped of it left the output in.
  private openCut = false
  private cutStyle: AnsiStyle = { ...PLAIN }
  // A carriage return a chunk ended with, which may yet start a CRLF.
  private pendingCR = false
  // The id of the open line as last shown, or -1 when there is none.
  private openId = -1
  private nextId = 0

  append(data: string) {
    if (!data) return
    let text = (this.pendingCR ? '\r' : '') + data
    this.pendingCR = text.endsWith('\r')
    if (this.pendingCR) text = text.slice(0, -1)
    text = text.replace(/\r\n/g, '\n')
    this.unshowOpen()
    const pieces = text.split('\n')
    for (let i = 0; i < pieces.length; i++) {
      this.feed(pieces[i])
      if (i < pieces.length - 1) this.closeLine()
    }
    this.showOpen()
    this.trim()
  }

  // gap ends the open line and marks that output was lost after it, so text
  // from either side of the gap is never joined into one line.
  gap() {
    this.unshowOpen()
    if (this.open || this.openCut) this.closeLine()
    this.pendingCR = false
    this.lines.push({ id: this.nextId++, segments: [], cut: false, gap: true })
    this.trim()
  }

  clear() {
    this.lines = []
    this.droppedLines = 0
    this.open = ''
    this.openStyle = { ...PLAIN }
    this.openCut = false
    this.cutStyle = { ...PLAIN }
    this.pendingCR = false
    this.openId = -1
  }

  // feed adds text with no newline to the open line.
  private feed(text: string) {
    const cr = text.lastIndexOf('\r')
    if (cr !== -1) {
      // Everything before the last carriage return is drawn over: only the
      // style it leaves is kept.
      this.openStyle = advanceStyle(text.slice(0, cr), this.endStyle())
      this.open = ''
      this.openCut = false
      text = text.slice(cr + 1)
    }
    if (this.openCut) {
      this.cutStyle = advanceStyle(text, this.cutStyle)
      return
    }
    const room = MAX_OPEN_CHARS - this.open.length
    if (text.length <= room) {
      this.open += text
      return
    }
    this.open += text.slice(0, room)
    this.openCut = true
    this.cutStyle = advanceStyle(text.slice(room), parseLine(this.open, this.openStyle).style)
  }

  // endStyle is the style the open line leaves the output in.
  private endStyle(): AnsiStyle {
    return this.openCut ? this.cutStyle : parseLine(this.open, this.openStyle).style
  }

  private closeLine() {
    const parsed = parseLine(this.open, this.openStyle)
    this.lines.push(this.line(parsed.segments, this.nextId++, this.openCut))
    this.openStyle = this.openCut ? this.cutStyle : parsed.style
    this.open = ''
    this.openCut = false
  }

  private unshowOpen() {
    const last = this.lines[this.lines.length - 1]
    if (last && last.id === this.openId) this.lines.pop()
    this.openId = -1
  }

  private showOpen() {
    if (!this.open && !this.openCut) return
    this.openId = this.nextId++
    this.lines.push(this.line(parseLine(this.open, this.openStyle).segments, this.openId, this.openCut))
  }

  private trim() {
    const over = this.lines.length - MAX_LOG_LINES
    if (over > 0) {
      this.lines.splice(0, over)
      this.droppedLines += over
    }
  }

  private line(segments: AnsiSegment[], id: number, cut: boolean): LogLine {
    let left = MAX_LINE_CHARS
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
