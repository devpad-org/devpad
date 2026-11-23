// Turns terminal output, which a job or anything else devpad doesn't
// trust wrote, into plain text spans: the colours and bold of its SGR
// escape codes become class names, every other escape sequence and control
// character is dropped, and nothing in it is ever read as HTML.

export type AnsiColor = 'black' | 'red' | 'green' | 'yellow' | 'blue' | 'magenta' | 'cyan' | 'white'

// How text is drawn at a point in the output.
export interface AnsiStyle {
  fg: AnsiColor | ''
  bright: boolean
  bold: boolean
}

export interface AnsiSegment {
  text: string
  // Class names for the segment, empty for plain text.
  cls: string
}

export const PLAIN: Readonly<AnsiStyle> = Object.freeze<AnsiStyle>({ fg: '', bright: false, bold: false })

const COLORS: AnsiColor[] = ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white']

// CSI (ESC [ ... final), OSC (ESC ] ... BEL or ESC \), and any other
// two-character escape. Group 1 holds a CSI's parameters, group 2 its final
// byte.
const ESCAPE = /\x1b\[([0-9;:?<=>]*)[ -/]*([@-~])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]/g
// An escape a chunk of output ends part way through, not yet complete.
const PARTIAL_ESCAPE = /\x1b(?:\[[0-9;:?<=>]*[ -/]*|\][^\x07\x1b]*)?$/
// Control characters other than tab, which a line of text never shows:
// C0, DEL and C1, and the bidirectional controls, with which output could
// draw its text in another order than it has.
const CONTROL = /[\x00-\x08\x0b-\x1f\x7f-\x9f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]/g

function applySgr(style: AnsiStyle, params: string): AnsiStyle {
  const next = { ...style }
  const codes = params === '' ? [0] : params.split(/[;:]/).map((p) => (p === '' ? 0 : Number(p)))
  for (let i = 0; i < codes.length; i++) {
    const c = codes[i]
    if (c === 0) Object.assign(next, PLAIN)
    else if (c === 1) next.bold = true
    else if (c === 22) next.bold = false
    else if (c >= 30 && c <= 37) Object.assign(next, { fg: COLORS[c - 30], bright: false })
    else if (c >= 90 && c <= 97) Object.assign(next, { fg: COLORS[c - 90], bright: true })
    else if (c === 39) Object.assign(next, { fg: '', bright: false })
    // 256-colour and true-colour codes carry arguments, skipped with them;
    // their colours aren't drawn, since they may not read on the theme.
    else if (c === 38 || c === 48 || c === 58) i += codes[i + 1] === 5 ? 2 : codes[i + 1] === 2 ? 4 : 1
  }
  return next
}

function classOf(style: AnsiStyle): string {
  const parts: string[] = []
  if (style.fg) parts.push(`ansi-${style.bright ? 'bright-' : ''}${style.fg}`)
  if (style.bold) parts.push('ansi-bold')
  return parts.join(' ')
}

// advanceStyle is the style text leaves output in, starting in style, for
// text that is not shown.
export function advanceStyle(text: string, style: AnsiStyle): AnsiStyle {
  let current = style
  ESCAPE.lastIndex = 0
  for (let m = ESCAPE.exec(text); m !== null; m = ESCAPE.exec(text)) {
    if (m[2] === 'm') current = applySgr(current, m[1] ?? '')
  }
  return current
}

// parseLine splits one line of output, without its newline, into styled
// segments, starting in style; it returns them with the style the line
// ends in, which the next line starts in. An escape cut off at the end of
// the line is dropped.
export function parseLine(line: string, style: AnsiStyle): { segments: AnsiSegment[]; style: AnsiStyle } {
  const text = (line.endsWith('\r') ? line.slice(0, -1) : line).replace(PARTIAL_ESCAPE, '')
  let segments: AnsiSegment[] = []
  let current = style
  let last = 0
  const push = (raw: string) => {
    // A carriage return within a line redraws it, as a progress bar does:
    // only what was drawn after the last one shows.
    const cr = raw.lastIndexOf('\r')
    if (cr !== -1) {
      segments = []
      raw = raw.slice(cr + 1)
    }
    const clean = raw.replace(CONTROL, '')
    if (!clean) return
    const cls = classOf(current)
    const prev = segments[segments.length - 1]
    if (prev && prev.cls === cls) prev.text += clean
    else segments.push({ text: clean, cls })
  }
  ESCAPE.lastIndex = 0
  for (let m = ESCAPE.exec(text); m !== null; m = ESCAPE.exec(text)) {
    push(text.slice(last, m.index))
    if (m[2] === 'm') current = applySgr(current, m[1] ?? '')
    last = m.index + m[0].length
  }
  push(text.slice(last))
  return { segments, style: current }
}
