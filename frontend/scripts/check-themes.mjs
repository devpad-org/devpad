// Checks every theme in src/assets/styles/themes.css for WCAG contrast:
// text, hue and syntax tokens against the surfaces they are drawn on.
// Run with `npm run check-themes`; exits non-zero when a pair falls short.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const css = readFileSync(fileURLToPath(new URL('../src/assets/styles/themes.css', import.meta.url)), 'utf8')
  .replace(/\/\*[\s\S]*?\*\//g, '')

// selector list → declarations, for each rule in the file.
const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(([, selectors, body]) => ({
  selectors: selectors.split(',').map((s) => s.trim()),
  tokens: Object.fromEntries([...body.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)].map(([, k, v]) => [k, v.trim()])),
}))

const themeIds = [...new Set(rules.flatMap((r) => r.selectors).map((s) => s.match(/^\[data-theme='([\w-]+)'\]$/)?.[1]).filter(Boolean))]
const shared = rules.filter((r) => r.selectors.includes('[data-theme]')).map((r) => r.tokens)

function themeTokens(id) {
  const own = rules.filter((r) => r.selectors.includes(`[data-theme='${id}']`)).map((r) => r.tokens)
  const raw = Object.assign({}, ...own, ...shared)
  const resolve = (value, depth = 0) =>
    depth > 8 ? value : value.replace(/var\((--[\w-]+)\)/g, (_, name) => resolve(raw[name] ?? 'MISSING', depth + 1))
  return Object.fromEntries(Object.entries(raw).map(([k, v]) => [k, resolve(v)]))
}

function parse(color) {
  let m = color.match(/^#([0-9a-f]{6})([0-9a-f]{2})?$/i)
  if (m) {
    const n = parseInt(m[1], 16)
    return [n >> 16, (n >> 8) & 255, n & 255, m[2] ? parseInt(m[2], 16) / 255 : 1]
  }
  m = color.match(/^rgba?\(\s*([\d.]+),\s*([\d.]+),\s*([\d.]+)(?:,\s*([\d.]+))?\s*\)$/)
  if (m) return [Number(m[1]), Number(m[2]), Number(m[3]), m[4] === undefined ? 1 : Number(m[4])]
  throw new Error(`cannot parse colour "${color}"`)
}

// `top` drawn over the opaque `bottom`.
function over(top, bottom) {
  const [r, g, b, a] = top
  return [r * a + bottom[0] * (1 - a), g * a + bottom[1] * (1 - a), b * a + bottom[2] * (1 - a), 1]
}

function luminance([r, g, b]) {
  const lin = (v) => ((v /= 255) <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4)
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b)
}

function contrast(fg, bg) {
  const [a, b] = [luminance(over(fg, bg)), luminance(bg)].sort((x, y) => y - x)
  return (a + 0.05) / (b + 0.05)
}

const SURFACES = ['--bg-void', '--bg-base', '--bg-elevated', '--bg-raised']
const TEXT = ['--text-primary', '--text-secondary', '--text-tertiary']
const HUES = ['--hue-red', '--hue-green', '--hue-yellow', '--hue-blue', '--hue-purple', '--hue-aqua', '--hue-orange', '--accent']
const FILLS = ['--accent', '--hue-blue', '--success', '--error', '--warning', '--attention']
const TINTS = ['error', 'success', 'warning', 'attention']
const TERM = ['fg', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white', 'bright-black', 'bright-red', 'bright-green', 'bright-yellow', 'bright-blue', 'bright-magenta', 'bright-cyan', 'bright-white']

const verbose = process.argv.includes('--verbose')
let failures = 0

for (const id of themeIds) {
  const t = themeTokens(id)
  const missing = Object.entries(t).filter(([, v]) => v.includes('MISSING')).map(([k]) => k)
  const lines = []
  const check = (fgName, bgName, min, bg = parse(t[bgName])) => {
    const ratio = contrast(parse(t[fgName]), bg)
    const ok = ratio >= min
    if (!ok) failures++
    if (!ok || verbose) lines.push(`  ${ok ? 'ok  ' : 'FAIL'} ${ratio.toFixed(2)} (min ${min})  ${fgName} on ${bgName}`)
  }

  for (const surface of SURFACES) {
    // Body text and coloured text: AA (4.5:1). Dim text is for placeholders,
    // disabled and decorative marks, held to the 3:1 of large text and UI parts.
    for (const name of TEXT) check(name, surface, 4.5)
    // The raised surface is for chips and small controls, where coloured text
    // is a short label; there it is held to 3:1.
    for (const name of HUES) check(name, surface, surface === '--bg-raised' ? 3 : 4.5)
    check('--text-dim', surface, 3)
  }
  // Selected rows and hovered rows sit on a wash over a surface.
  for (const wash of ['--bg-selected', '--bg-hover']) {
    for (const surface of SURFACES.slice(0, 3)) {
      const bg = over(parse(t[wash]), parse(t[surface]))
      for (const name of ['--text-primary', '--text-secondary', '--accent']) check(name, `${wash} over ${surface}`, 4.5, bg)
    }
  }
  // Alerts and badges: a semantic colour on its own tint.
  for (const tint of TINTS) {
    for (const surface of SURFACES.slice(0, 3)) {
      check(`--${tint}`, `--${tint}-bg over ${surface}`, 4.5, over(parse(t[`--${tint}-bg`]), parse(t[surface])))
    }
  }
  // Filled buttons.
  for (const fill of FILLS) check('--text-on-accent', fill, 4.5)
  // Editor.
  for (const name of Object.keys(t).filter((k) => k.startsWith('--syntax-'))) check(name, '--editor-bg', 4.5)
  check('--editor-fg', '--editor-bg', 7)
  check('--editor-fg', '--editor-selection', 4.5)
  check('--editor-line-number-active', '--editor-bg', 4.5)
  check('--editor-line-number', '--editor-bg', 2.5)
  check('--editor-cursor', '--editor-bg', 3)
  // Terminal. xterm lifts anything dimmer to 4.5 itself (minimumContrastRatio),
  // so this only keeps the palette from leaning on that.
  for (const name of TERM) check(`--term-${name}`, '--terminal-bg', name === 'fg' ? 7 : 2)
  check('--term-cursor', '--terminal-bg', 3)

  const bad = lines.filter((l) => l.includes('FAIL')).length + missing.length
  console.log(`${bad ? '✗' : '✓'} ${id}`)
  for (const name of missing) {
    failures++
    console.log(`  FAIL ${name} refers to a token the theme does not define`)
  }
  for (const line of lines) console.log(line)
}

// Every theme defines the same tokens.
const names = themeIds.map((id) => new Set(Object.keys(themeTokens(id))))
const all = new Set(names.flatMap((s) => [...s]))
themeIds.forEach((id, i) => {
  for (const name of all) {
    if (!names[i].has(name)) {
      failures++
      console.log(`✗ ${id} does not define ${name}`)
    }
  }
})

if (failures) {
  console.log(`\n${failures} problem${failures === 1 ? '' : 's'}`)
  process.exit(1)
}
console.log('\nAll themes pass.')
