// The app's colour themes. The colours themselves are CSS tokens in
// assets/styles/themes.css, chosen by `data-theme` on <html>; this module
// picks the theme, remembers it, and builds the Monaco and xterm themes from
// the same tokens so the editor and terminal can never drift from the UI.

import { ref, readonly } from 'vue'
import type * as MonacoType from 'monaco-editor'
import type { ITheme } from '@xterm/xterm'

// Each id is the `data-theme` value its block in assets/styles/themes.css is
// chosen by. The server keeps the same list (settings.Themes); a Go test and
// `npm run check-themes` fail when the three disagree.
export const THEMES = [
  { id: 'gruvbox-dark', label: 'Gruvbox Dark', description: 'Warm, retro and easy on the eyes. The default.' },
  { id: 'gruvbox-light', label: 'Gruvbox Light', description: 'The same warm palette on cream paper.' },
  { id: 'modern-dark', label: 'Modern Dark', description: 'Cool, neutral surfaces with a blue accent.' },
  { id: 'modern-light', label: 'Modern Light', description: 'Clean and bright, with a blue accent.' },
] as const satisfies readonly { id: string; label: string; description: string }[]

export type ThemeId = (typeof THEMES)[number]['id']

export const DEFAULT_THEME: ThemeId = 'gruvbox-dark'

// Also read by the inline script in index.html, which applies the theme
// before the first paint.
const STORAGE_KEY = 'devpad-theme'

export function isThemeId(value: unknown): value is ThemeId {
  return THEMES.some((t) => t.id === value)
}

function storedTheme(): ThemeId {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (isThemeId(stored)) return stored
  } catch {
    // Storage can be unavailable (private mode); the default still works.
  }
  return DEFAULT_THEME
}

const current = ref<ThemeId>(storedTheme())

// How many times the theme has been switched, here or in another tab.
let switches = 0

/** Counts every switch of theme, so a caller can tell whether one happened while it waited. */
export function themeSwitchCount(): number {
  return switches
}

/** The theme in use. Changes when the user picks another, here or in another tab. */
export const currentTheme = readonly(current)

// ── Reading tokens ─────────────────────────────────────────────────────────

function tokens(): (name: string) => string {
  const style = getComputedStyle(document.documentElement)
  return (name) => style.getPropertyValue(name).trim()
}

// Monaco takes only hex colours, and some UI tokens are rgb()/rgba().
let canvas: CanvasRenderingContext2D | null | undefined

// Any CSS colour as the browser's own #rrggbb or rgba(): the production
// build minifies the tokens, so #ffffff arrives as #fff and a hex colour
// may arrive as a colour name.
function normalise(color: string): string {
  if (canvas === undefined) canvas = document.createElement('canvas').getContext('2d')
  if (!canvas) return color
  canvas.fillStyle = '#000000'
  canvas.fillStyle = color
  return canvas.fillStyle
}

// A CSS colour as #rrggbb or #rrggbbaa, or null when it is missing or is not
// one this can turn into hex (color-mix(), oklch() and the like).
function hex(color: string): string | null {
  if (/^#[0-9a-f]{6}([0-9a-f]{2})?$/i.test(color)) return color
  if (/^#[0-9a-f]{3,4}$/i.test(color)) return '#' + [...color.slice(1)].map((d) => d + d).join('')
  if (!color || !CSS.supports('color', color)) return null
  const value = normalise(color)
  if (/^#[0-9a-f]{6}$/i.test(value)) return value
  const m = value.match(/^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)(?:[\s,/]+([\d.]+))?\s*\)$/)
  if (!m) return null
  const byte = (n: number) => Math.round(Math.min(255, Math.max(0, n))).toString(16).padStart(2, '0')
  const alpha = m[4] === undefined ? '' : byte(Number(m[4]) * 255)
  return `#${byte(Number(m[1]))}${byte(Number(m[2]))}${byte(Number(m[3]))}${alpha}`
}

// A colour token as hex, for Monaco and the browser's chrome. A token that is
// missing or unreadable becomes black, so say so while developing; `npm run
// check-themes` refuses such a token at build time.
function tokenHex(t: (name: string) => string, name: string): string {
  const value = hex(t(name))
  if (value) return value
  if (import.meta.env.DEV) {
    const raw = t(name)
    console.warn(`[theme] ${current.value}: ${name} ${raw ? `is "${raw}", which is not a colour the editor theme can use` : 'is not defined'}; using black`)
  }
  return '#000000'
}

// ── Terminal ───────────────────────────────────────────────────────────────

export const terminalFontFamily = "ui-monospace, 'SFMono-Regular', Menlo, Monaco, 'Cascadia Mono', 'DejaVu Sans Mono', 'Liberation Mono', Consolas, 'Courier New', monospace"

/** The xterm theme for the theme in use. */
export function terminalTheme(): ITheme {
  const t = tokens()
  return {
    background: t('--terminal-bg'),
    foreground: t('--term-fg'),
    cursor: t('--term-cursor'),
    cursorAccent: t('--terminal-bg'),
    selectionBackground: t('--term-selection'),
    black: t('--term-black'),
    red: t('--term-red'),
    green: t('--term-green'),
    yellow: t('--term-yellow'),
    blue: t('--term-blue'),
    magenta: t('--term-magenta'),
    cyan: t('--term-cyan'),
    white: t('--term-white'),
    brightBlack: t('--term-bright-black'),
    brightRed: t('--term-bright-red'),
    brightGreen: t('--term-bright-green'),
    brightYellow: t('--term-bright-yellow'),
    brightBlue: t('--term-bright-blue'),
    brightMagenta: t('--term-bright-magenta'),
    brightCyan: t('--term-bright-cyan'),
    brightWhite: t('--term-bright-white'),
  }
}

// ── Monaco ─────────────────────────────────────────────────────────────────

/** The one Monaco theme name; it is redefined whenever the app's theme changes. */
export const DEVPAD_MONACO_THEME = 'devpad'

// Monaco token → syntax token in themes.css. The theme does not inherit its
// base's rules: those are more specific than these in places (HTML attribute
// values, JSON keys) and would show through in the base's own colours.
const SYNTAX_RULES: [token: string, syntax: string, fontStyle?: string][] = [
  ['', 'variable'],
  ['emphasis', 'variable', 'italic'],
  ['strong', 'variable', 'bold'],
  ['comment', 'comment', 'italic'],
  ['comment.doc', 'comment-doc', 'italic'],
  ['keyword', 'keyword'],
  ['keyword.control', 'keyword'],
  ['keyword.operator', 'operator'],
  ['storage', 'storage'],
  ['storage.type', 'type'],
  ['string', 'string'],
  ['string.escape', 'escape', 'bold'],
  ['string.template', 'string'],
  ['number', 'number'],
  ['number.float', 'number'],
  ['number.hex', 'language'],
  ['type', 'type'],
  ['type.identifier', 'type'],
  ['class', 'type'],
  ['class.name', 'type'],
  ['interface', 'type'],
  ['function', 'function'],
  ['function.call', 'function'],
  ['method', 'function'],
  ['method.call', 'function'],
  ['variable', 'variable'],
  ['variable.name', 'variable'],
  ['variable.parameter', 'parameter'],
  ['variable.language', 'language'],
  ['constant', 'number'],
  ['constant.language', 'language'],
  ['constant.numeric', 'number'],
  ['tag', 'tag'],
  ['tag.id', 'tag'],
  ['tag.class', 'tag-class'],
  ['metatag', 'storage'],
  ['attribute.name', 'type'],
  ['attribute.value', 'string'],
  ['delimiter', 'delimiter'],
  ['delimiter.bracket', 'bracket'],
  ['delimiter.curly', 'curly'],
  ['delimiter.parenthesis', 'delimiter'],
  ['operator', 'operator'],
  ['operator.assignment', 'operator'],
  ['identifier', 'variable'],
  ['namespace', 'function'],
  ['decorator', 'language'],
  ['annotation', 'language'],
  ['regexp', 'string'],
  ['invalid', 'invalid', 'underline'],
  ['key', 'tag'],
  ['string.key.json', 'function'],
  ['string.value.json', 'string'],
  ['keyword.json', 'language'],
  ['meta', 'storage'],
  ['predefined', 'function'],
  ['variable.predefined', 'language'],
  ['attribute.value.number', 'number'],
  ['attribute.value.unit', 'number'],
  ['attribute.value.hex', 'language'],
]

function monacoTheme(): MonacoType.editor.IStandaloneThemeData {
  const t = tokens()
  const c = (name: string) => tokenHex(t, name)
  const transparent = '#00000000'

  return {
    // Light or dark comes from the theme's color-scheme in themes.css.
    base: t('color-scheme') === 'dark' ? 'vs-dark' : 'vs',
    inherit: false,
    rules: SYNTAX_RULES.map(([token, syntax, fontStyle]) => ({
      token,
      foreground: c(`--syntax-${syntax}`).slice(1),
      ...(fontStyle ? { fontStyle } : {}),
    })),
    colors: {
      foreground: c('--text-primary'),
      descriptionForeground: c('--text-secondary'),
      disabledForeground: c('--text-dim'),
      errorForeground: c('--error'),
      focusBorder: c('--accent-border'),
      'icon.foreground': c('--text-secondary'),
      'textLink.foreground': c('--accent'),
      'textLink.activeForeground': c('--accent'),

      'editor.background': c('--editor-bg'),
      'editor.foreground': c('--editor-fg'),
      'editor.lineHighlightBackground': c('--editor-line-highlight'),
      'editor.lineHighlightBorder': transparent,
      'editor.selectionBackground': c('--editor-selection'),
      'editor.inactiveSelectionBackground': c('--editor-selection-inactive'),
      'editor.selectionHighlightBackground': c('--editor-selection-highlight'),
      'editor.wordHighlightBackground': c('--editor-selection-highlight'),
      'editor.wordHighlightStrongBackground': c('--editor-find-match-highlight'),
      'editor.findMatchBackground': c('--editor-find-match'),
      'editor.findMatchHighlightBackground': c('--editor-find-match-highlight'),
      'editor.findRangeHighlightBackground': c('--editor-selection-highlight'),
      'editor.hoverHighlightBackground': c('--editor-selection-highlight'),
      'editorLink.activeForeground': c('--accent'),
      'editorLineNumber.foreground': c('--editor-line-number'),
      'editorLineNumber.activeForeground': c('--editor-line-number-active'),
      'editorCursor.foreground': c('--editor-cursor'),
      'editorWhitespace.foreground': c('--editor-indent-guide'),
      'editorIndentGuide.background1': c('--editor-indent-guide'),
      'editorIndentGuide.activeBackground1': c('--editor-indent-guide-active'),
      'editorRuler.foreground': c('--editor-indent-guide'),
      'editorBracketMatch.background': c('--editor-bracket-match-bg'),
      'editorBracketMatch.border': c('--editor-bracket-match-border'),
      'editorBracketHighlight.foreground1': c('--syntax-bracket'),
      'editorBracketHighlight.foreground2': c('--syntax-curly'),
      'editorBracketHighlight.foreground3': c('--syntax-type'),
      'editorBracketHighlight.unexpectedBracket.foreground': c('--syntax-invalid'),
      'editorGutter.background': c('--editor-bg'),
      'editorGutter.addedBackground': c('--success'),
      'editorGutter.modifiedBackground': c('--hue-blue'),
      'editorGutter.deletedBackground': c('--error'),
      'editorError.foreground': c('--error'),
      'editorWarning.foreground': c('--warning'),
      'editorInfo.foreground': c('--hue-blue'),
      'editorHint.foreground': c('--hue-aqua'),
      'editorCodeLens.foreground': c('--text-dim'),
      'editorGhostText.foreground': c('--text-dim'),
      'editorOverviewRuler.border': transparent,
      'editorOverviewRuler.errorForeground': c('--error'),
      'editorOverviewRuler.warningForeground': c('--warning'),
      'editorOverviewRuler.infoForeground': c('--hue-blue'),
      'editorOverviewRuler.findMatchForeground': c('--editor-find-match'),
      'editorStickyScroll.background': c('--editor-bg'),
      'editorStickyScrollHover.background': c('--bg-elevated'),

      'diffEditor.insertedTextBackground': c('--editor-diff-inserted'),
      'diffEditor.removedTextBackground': c('--editor-diff-removed'),
      'diffEditor.insertedLineBackground': c('--editor-diff-inserted'),
      'diffEditor.removedLineBackground': c('--editor-diff-removed'),
      'diffEditor.diagonalFill': c('--border-subtle'),
      'diffEditor.border': c('--border-subtle'),
      'diffEditor.unchangedRegionBackground': c('--bg-elevated'),
      'diffEditor.unchangedRegionForeground': c('--text-secondary'),
      'diffEditor.unchangedCodeBackground': c('--bg-hover'),
      'diffEditorGutter.insertedLineBackground': c('--editor-diff-inserted'),
      'diffEditorGutter.removedLineBackground': c('--editor-diff-removed'),
      'diffEditorOverview.insertedForeground': c('--success-border'),
      'diffEditorOverview.removedForeground': c('--error-border'),

      'minimap.background': c('--editor-bg'),
      'minimapGutter.background': c('--editor-bg'),
      'minimapSlider.background': c('--editor-scrollbar'),
      'minimapSlider.hoverBackground': c('--editor-scrollbar-hover'),
      'minimapSlider.activeBackground': c('--editor-scrollbar-active'),
      'scrollbar.shadow': transparent,
      'scrollbarSlider.background': c('--editor-scrollbar'),
      'scrollbarSlider.hoverBackground': c('--editor-scrollbar-hover'),
      'scrollbarSlider.activeBackground': c('--editor-scrollbar-active'),

      // Find, hover, suggestions, menus and the command palette.
      'widget.shadow': transparent,
      'widget.border': c('--border-strong'),
      'editorWidget.background': c('--bg-elevated'),
      'editorWidget.foreground': c('--text-primary'),
      'editorWidget.border': c('--border-strong'),
      'editorWidget.resizeBorder': c('--accent-border'),
      'editorHoverWidget.background': c('--bg-elevated'),
      'editorHoverWidget.foreground': c('--text-primary'),
      'editorHoverWidget.border': c('--border-strong'),
      'editorHoverWidget.statusBarBackground': c('--bg-raised'),
      'editorSuggestWidget.background': c('--bg-elevated'),
      'editorSuggestWidget.foreground': c('--text-primary'),
      'editorSuggestWidget.border': c('--border-strong'),
      'editorSuggestWidget.selectedBackground': c('--bg-raised'),
      'editorSuggestWidget.selectedForeground': c('--text-primary'),
      'editorSuggestWidget.highlightForeground': c('--accent'),
      'editorSuggestWidget.focusHighlightForeground': c('--accent'),
      'peekView.border': c('--accent-border'),
      'peekViewEditor.background': c('--bg-base'),
      'peekViewEditorGutter.background': c('--bg-base'),
      'peekViewResult.background': c('--bg-elevated'),
      'peekViewTitle.background': c('--bg-elevated'),
      'peekViewTitleLabel.foreground': c('--text-primary'),
      'peekViewTitleDescription.foreground': c('--text-secondary'),
      'input.background': c('--bg-base'),
      'input.foreground': c('--text-primary'),
      'input.border': c('--border-subtle'),
      'input.placeholderForeground': c('--text-dim'),
      'inputOption.activeBackground': c('--accent-glow'),
      'inputOption.activeBorder': c('--accent-border'),
      'inputOption.activeForeground': c('--text-primary'),
      'inputOption.hoverBackground': c('--bg-hover'),
      'inputValidation.errorBackground': c('--bg-elevated'),
      'inputValidation.errorBorder': c('--error'),
      'inputValidation.warningBackground': c('--bg-elevated'),
      'inputValidation.warningBorder': c('--warning'),
      'inputValidation.infoBackground': c('--bg-elevated'),
      'inputValidation.infoBorder': c('--hue-blue'),
      'dropdown.background': c('--bg-elevated'),
      'dropdown.foreground': c('--text-primary'),
      'dropdown.border': c('--border-subtle'),
      'button.background': c('--accent'),
      'button.foreground': c('--text-on-accent'),
      'button.hoverBackground': c('--accent-dim'),
      'badge.background': c('--bg-raised'),
      'badge.foreground': c('--text-primary'),
      'progressBar.background': c('--accent'),
      'toolbar.hoverBackground': c('--bg-hover'),
      'toolbar.activeBackground': c('--bg-selected'),
      'list.hoverBackground': c('--bg-hover'),
      'list.hoverForeground': c('--text-primary'),
      'list.focusBackground': c('--bg-raised'),
      'list.focusForeground': c('--text-primary'),
      'list.activeSelectionBackground': c('--bg-raised'),
      'list.activeSelectionForeground': c('--text-primary'),
      'list.inactiveSelectionBackground': c('--bg-raised'),
      'list.inactiveSelectionForeground': c('--text-primary'),
      'list.highlightForeground': c('--accent'),
      'list.focusHighlightForeground': c('--accent'),
      'menu.background': c('--bg-elevated'),
      'menu.foreground': c('--text-primary'),
      'menu.border': c('--border-strong'),
      'menu.selectionBackground': c('--bg-raised'),
      'menu.selectionForeground': c('--text-primary'),
      'menu.separatorBackground': c('--border-subtle'),
      'quickInput.background': c('--bg-elevated'),
      'quickInput.foreground': c('--text-primary'),
      'quickInputTitle.background': c('--bg-raised'),
      'quickInputList.focusBackground': c('--bg-raised'),
      'quickInputList.focusForeground': c('--text-primary'),
      'pickerGroup.foreground': c('--accent'),
      'pickerGroup.border': c('--border-subtle'),
      'keybindingLabel.background': c('--bg-raised'),
      'keybindingLabel.foreground': c('--text-primary'),
      'keybindingLabel.border': c('--border-strong'),
      'keybindingLabel.bottomBorder': c('--border-strong'),
    },
  }
}

let monaco: typeof MonacoType | null = null

/**
 * Defines the editor theme from the app's theme. Called by whoever loads
 * Monaco, before creating an editor with `theme: DEVPAD_MONACO_THEME`; from
 * then on the theme follows the app's.
 */
export function registerDevpadTheme(m: typeof MonacoType) {
  monaco = m
  m.editor.defineTheme(DEVPAD_MONACO_THEME, monacoTheme())
}

// ── Applying ───────────────────────────────────────────────────────────────

// The tab icon and the browser's own chrome follow the theme too.
function applyDocumentChrome() {
  const t = tokens()
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', tokenHex(t, '--bg-elevated'))

  const icon = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (icon) {
    const svg =
      `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">` +
      `<rect width="32" height="32" rx="7" fill="${t('--bg-elevated')}"/>` +
      `<rect x=".75" y=".75" width="30.5" height="30.5" rx="6.25" fill="none" stroke="${t('--accent')}" stroke-opacity=".45" stroke-width="1.5"/>` +
      `<text x="16" y="17.5" dominant-baseline="middle" text-anchor="middle" font-family="ui-monospace,Menlo,Consolas,monospace" font-size="19" font-weight="700" fill="${t('--accent')}">D</text>` +
      `</svg>`
    icon.href = `data:image/svg+xml,${encodeURIComponent(svg)}`
  }
}

function apply(id: ThemeId) {
  switches++
  document.documentElement.dataset.theme = id
  current.value = id
  applyDocumentChrome()
  if (monaco) {
    // Redefining the theme in use does not repaint, so set it again.
    monaco.editor.defineTheme(DEVPAD_MONACO_THEME, monacoTheme())
    monaco.editor.setTheme(DEVPAD_MONACO_THEME)
  }
}

/** Switches the app to a theme and remembers it in this browser. */
export function setTheme(id: ThemeId) {
  try {
    localStorage.setItem(STORAGE_KEY, id)
  } catch {
    // Not remembered, but still applied.
  }
  apply(id)
}

/** Applies the remembered theme and follows changes made in other tabs. Called once at startup. */
export function initTheme() {
  apply(current.value)
  window.addEventListener('storage', (e) => {
    if (e.key !== STORAGE_KEY) return
    apply(isThemeId(e.newValue) ? e.newValue : DEFAULT_THEME)
  })
}
