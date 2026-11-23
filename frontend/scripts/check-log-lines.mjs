// Checks src/utils/ansi.ts and src/utils/logLines.ts, which turn a pipeline
// job's output, which nothing trusts, into the lines the page shows:
//  - escape codes split across chunks, and a CRLF split across them, still
//    read as one;
//  - a line that never ends, and a progress bar redrawing one line with
//    carriage returns, hold a bounded line however long they go on, and
//    cost the same for each chunk (20 MB of each, within a time limit);
//  - colours carry over what is dropped of a line too long to show;
//  - lost output never joins the lines on either side of it;
//  - control characters, C1 and bidirectional controls never reach the page.
// Run with `npm run check-log-lines` (`npm run build` runs it too); exits
// non-zero on any problem.

import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import ts from 'typescript'

const dir = mkdtempSync(join(tmpdir(), 'devpad-log-lines-'))
for (const name of ['ansi', 'logLines']) {
  const source = readFileSync(fileURLToPath(new URL(`../src/utils/${name}.ts`, import.meta.url)), 'utf8')
  const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 } }).outputText
  writeFileSync(join(dir, `${name}.mjs`), js.replace("from './ansi'", "from './ansi.mjs'"))
}
const { PLAIN, parseLine } = await import(pathToFileURL(join(dir, 'ansi.mjs')).href)
const { LogLines, MAX_LINE_CHARS, MAX_LOG_LINES } = await import(pathToFileURL(join(dir, 'logLines.mjs')).href)
rmSync(dir, { recursive: true, force: true })

let failures = 0
function check(ok, message) {
  if (!ok) {
    failures++
    console.log(`✗ ${message}`)
  }
}
const text = (line) => line.segments.map((s) => s.text).join('')
const MB = 1 << 20

let log = new LogLines()
log.append('a\x1b[3')
log.append('2mgreen\r')
log.append('\nnext')
log.append(' more\n')
check(log.lines.length === 2 && text(log.lines[0]) === 'agreen' && log.lines[1].segments[0].cls === 'ansi-green' && text(log.lines[1]) === 'next more', 'an escape and a CRLF split across chunks')

log = new LogLines()
let start = performance.now()
const chunk = '\x1b[31m' + 'x'.repeat(64 * 1024 - 5)
for (let total = 0; total < 20 * MB; total += chunk.length) log.append(chunk)
let took = performance.now() - start
check(took < 3000, `20 MB without a newline took ${Math.round(took)} ms`)
check(log.lines.length === 1 && log.lines[0].cut && text(log.lines[0]).length === MAX_LINE_CHARS, 'a line that never ends is held cut')
log.append('\x1b[0mend\nplain\n')
check(log.lines.length === 2 && text(log.lines[1]) === 'plain' && log.lines[1].segments[0].cls === '', 'the style carries over what was dropped of a cut line')

log = new LogLines()
start = performance.now()
let pending = ''
let i = 0
for (let total = 0; total < 20 * MB; i++) {
  pending += `\r\x1b[32mprogress ${i}`
  if (pending.length >= 64 * 1024) {
    log.append(pending)
    total += pending.length
    pending = ''
  }
}
log.append(pending)
took = performance.now() - start
check(took < 3000, `20 MB of a progress bar took ${Math.round(took)} ms`)
check(log.lines.length === 1 && text(log.lines[0]) === `progress ${i - 1}` && log.lines[0].segments[0].cls === 'ansi-green', 'a progress bar shows its last redraw')
log.append('\ndone\n')
check(log.lines.length === 2 && text(log.lines[1]) === 'done', 'a progress bar ends with its line')

log = new LogLines()
log.append('before')
log.gap()
log.append('after\n')
check(log.lines.length === 3 && text(log.lines[0]) === 'before' && log.lines[1].gap && text(log.lines[2]) === 'after', 'lost output joins nothing')

const unsafe = parseLine('a‮b⁦c\x85d\x07e\x1b]0;title\x07f\x1b[2Kg<img src=x>', PLAIN).segments
check(unsafe.map((s) => s.text).join('') === 'abcdefg<img src=x>', 'control and bidirectional characters are dropped, markup stays text')
check(unsafe.every((s) => /^[a-z -]*$/.test(s.cls)), 'class names are only devpad’s')

log = new LogLines()
let lines = ''
for (let n = 0; n < MAX_LOG_LINES + 10; n++) lines += `line ${n}\n`
log.append(lines)
check(log.lines.length === MAX_LOG_LINES && log.droppedLines === 10 && text(log.lines[0]) === 'line 10', 'the oldest lines go past the cap')

if (failures > 0) {
  console.log(`\n${failures} problem${failures === 1 ? '' : 's'} in the log lines.`)
  process.exit(1)
}
console.log('Log lines pass.')
