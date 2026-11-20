// Checks the themes in src/assets/styles/themes.css:
//  - every theme sets color-scheme to light or dark;
//  - the themes there are the ones src/theme/index.ts offers (THEMES), and the
//    default is the same in both;
//  - every theme defines the same tokens;
//  - every colour the editor, terminal and browser chrome read from a theme is
//    defined and is a colour src/theme/index.ts can use (it turns any other
//    into black);
//  - text, hue and syntax tokens meet WCAG contrast on the surfaces they are
//    drawn on.
// Run with `npm run check-themes` (`npm run build` runs it too); exits
// non-zero on any problem. The server's own list of themes is checked against
// THEMES by a Go test (internal/settings).

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const read = (path) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), 'utf8')

const css = read('../src/assets/styles/themes.css').replace(/\/\*[\s\S]*?\*\//g, '')
const themeModule = read('../src/theme/index.ts')

let failures = 0
function fail(message) {
  failures++
  console.log(message)
}

// selector list → declarations, for each rule in the file.
const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(([, selectors, body]) => ({
  selectors: selectors.split(',').map((s) => s.trim()),
  tokens: Object.fromEntries([...body.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)].map(([, k, v]) => [k, v.trim()])),
  colorScheme: body.match(/(?:^|[;{\s])color-scheme\s*:\s*([^;]+);/)?.[1].trim(),
}))

const themeIdOf = (selector) => selector.match(/^\[data-theme='([\w-]+)'\]$/)?.[1]
const themeIds = [...new Set(rules.flatMap((r) => r.selectors).map(themeIdOf).filter(Boolean))]
const shared = rules.filter((r) => r.selectors.includes('[data-theme]')).map((r) => r.tokens)

function themeTokens(id) {
  const own = rules.filter((r) => r.selectors.includes(`[data-theme='${id}']`)).map((r) => r.tokens)
  const raw = Object.assign({}, ...own, ...shared)
  const resolve = (value, depth = 0) =>
    depth > 8 ? value : value.replace(/var\((--[\w-]+)\)/g, (_, name) => resolve(raw[name] ?? 'MISSING', depth + 1))
  return Object.fromEntries(Object.entries(raw).map(([k, v]) => [k, resolve(v)]))
}

// ── The theme list ─────────────────────────────────────────────────────────

console.log('Theme list')
const before = failures
const themesBlock = themeModule.match(/export const THEMES = \[([\s\S]*?)\n\]/)?.[1]
const listed = themesBlock ? [...themesBlock.matchAll(/\bid: '([\w-]+)'/g)].map((m) => m[1]) : []
const defaultTheme = themeModule.match(/export const DEFAULT_THEME: ThemeId = '([\w-]+)'/)?.[1]
// The block that is also :root is what shows before any theme is applied.
const rootThemes = rules.filter((r) => r.selectors.includes(':root')).flatMap((r) => r.selectors.map(themeIdOf)).filter(Boolean)

if (!listed.length) fail('  FAIL cannot find the ids in THEMES in src/theme/index.ts')
if (!defaultTheme) fail('  FAIL cannot find DEFAULT_THEME in src/theme/index.ts')
if (!themeIds.length) fail("  FAIL themes.css has no [data-theme='…'] blocks")
for (const id of themeIds) if (listed.length && !listed.includes(id)) fail(`  FAIL ${id} is in themes.css but not in THEMES, so it can never be chosen`)
for (const id of listed) if (!themeIds.includes(id)) fail(`  FAIL ${id} is in THEMES but has no [data-theme='${id}'] block in themes.css`)
if (defaultTheme && !listed.includes(defaultTheme)) fail(`  FAIL DEFAULT_THEME ${defaultTheme} is not in THEMES`)
if (rootThemes.length !== 1) fail(`  FAIL exactly one theme block must also be :root, found ${rootThemes.length}`)
else if (defaultTheme && rootThemes[0] !== defaultTheme) fail(`  FAIL the :root block is ${rootThemes[0]}, but DEFAULT_THEME is ${defaultTheme}`)
// :root and [data-theme='…'] are equally specific, so on <html> the later
// block wins: below another theme, the default's would override it.
const firstThemeRule = rules.find((r) => r.selectors.some(themeIdOf))
if (rootThemes.length === 1 && !firstThemeRule?.selectors.includes(':root')) {
  fail(`  FAIL the :root block (${rootThemes[0]}) must be the first theme block in themes.css, or it overrides every theme above it`)
}
if (failures === before) console.log(`  ok   ${themeIds.join(', ')}`)

// ── Every theme defines the same tokens ────────────────────────────────────

const tokensByTheme = Object.fromEntries(themeIds.map((id) => [id, themeTokens(id)]))
const all = new Set(themeIds.flatMap((id) => Object.keys(tokensByTheme[id])))
for (const id of themeIds) {
  for (const name of all) if (!(name in tokensByTheme[id])) fail(`✗ ${id} does not define ${name}`)
}

// ── Colours ────────────────────────────────────────────────────────────────

// The colours index.ts reads: every token it names, and the syntax token of
// each of its Monaco rules.
const syntaxBlock = themeModule.match(/const SYNTAX_RULES[^=]*= \[([\s\S]*?)\n\]/)?.[1] ?? ''
const moduleColours = [
  ...new Set([
    ...[...themeModule.matchAll(/'(--[\w-]+)'/g)].map((m) => m[1]),
    ...[...syntaxBlock.matchAll(/\[\s*'[^']*',\s*'([\w-]+)'/g)].map((m) => `--syntax-${m[1]}`),
  ]),
]
if (!syntaxBlock) fail('✗ cannot find SYNTAX_RULES in src/theme/index.ts')

// A colour as [r, g, b, a], or null when it is not hex or rgb()/rgba().
function parse(color) {
  let m = color.match(/^#([0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})$/i)
  if (m) {
    let digits = m[1]
    if (digits.length <= 4) digits = [...digits].map((d) => d + d).join('')
    const n = parseInt(digits.slice(0, 6), 16)
    return [n >> 16, (n >> 8) & 255, n & 255, digits.length === 8 ? parseInt(digits.slice(6), 16) / 255 : 1]
  }
  m = color.match(/^rgba?\(\s*([\d.]+)\s*(?:,\s*|\s+)([\d.]+)\s*(?:,\s*|\s+)([\d.]+)\s*(?:[,/]\s*([\d.]+)(%?))?\s*\)$/)
  if (m) {
    const alpha = m[4] === undefined ? 1 : Number(m[4]) / (m[5] ? 100 : 1)
    return [Number(m[1]), Number(m[2]), Number(m[3]), alpha]
  }
  return null
}

// `top` drawn over the opaque `bottom`.
function over(top, bottom) {
  if (!top || !bottom) return null
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

for (const id of themeIds) {
  const t = tokensByTheme[id]
  // Problems with a token itself, reported once each, before its contrast.
  const problems = new Map()
  for (const [name, value] of Object.entries(t)) {
    if (value.includes('MISSING')) problems.set(name, 'refers to a token the theme does not define')
  }
  // Whether a theme is light or dark is its color-scheme: the browser draws
  // form controls and scrollbars by it, and the editor picks its base by it.
  const schemes = rules.filter((r) => r.selectors.includes(`[data-theme='${id}']`) && r.colorScheme).map((r) => r.colorScheme)
  const scheme = schemes.at(-1)
  if (scheme !== 'light' && scheme !== 'dark') {
    problems.set('color-scheme', scheme ? `is "${scheme}", but must be light or dark` : 'is not set, but must be light or dark')
  }
  const color = (name) => {
    if (problems.has(name)) return null
    if (!(name in t)) {
      problems.set(name, 'is not defined')
      return null
    }
    const parsed = parse(t[name])
    if (!parsed) problems.set(name, `is "${t[name]}", which is not a hex or rgb() colour`)
    return parsed
  }

  // The editor, terminal and browser chrome take these as colours.
  for (const name of moduleColours) color(name)
  for (const name of Object.keys(t).filter((k) => /^--(editor|syntax|term)-/.test(k))) color(name)

  const lines = []
  const check = (fgName, bgName, min, bg = color(bgName)) => {
    const fg = color(fgName)
    if (!fg || !bg) return // the token's problem is reported instead
    const ratio = contrast(fg, bg)
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
      const bg = over(color(wash), color(surface))
      for (const name of ['--text-primary', '--text-secondary', '--accent']) check(name, `${wash} over ${surface}`, 4.5, bg)
    }
  }
  // Alerts and badges: a semantic colour on its own tint.
  for (const tint of TINTS) {
    for (const surface of SURFACES.slice(0, 3)) {
      check(`--${tint}`, `--${tint}-bg over ${surface}`, 4.5, over(color(`--${tint}-bg`), color(surface)))
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

  failures += problems.size
  const bad = lines.filter((l) => l.includes('FAIL')).length + problems.size
  console.log(`${bad ? '✗' : '✓'} ${id}`)
  for (const [name, problem] of problems) console.log(`  FAIL ${name} ${problem}`)
  for (const line of lines) console.log(line)
}

if (failures) {
  console.log(`\n${failures} problem${failures === 1 ? '' : 's'}`)
  process.exit(1)
}
console.log('\nAll themes pass.')
