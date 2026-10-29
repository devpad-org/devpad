// A project's emblem: a 2x2 Bauhaus-style tile drawn from the Gruvbox
// palette, generated from the project's name so it stays the same everywhere
// and needs nothing stored. Each cell is a coloured square with one shape on
// it; the emblem's hue also tints the project's card.

export type EmblemShape = 'quarter' | 'half' | 'circle' | 'ring' | 'triangle' | 'leaf' | 'bars'

export interface EmblemCell {
  x: number
  y: number
  shape: EmblemShape
  // Quarter turns about the cell's centre.
  rotation: number
  background: string
  foreground: string
}

export interface Emblem {
  // The emblem's main colour, as a CSS value.
  hue: string
  cells: EmblemCell[]
}

export const EMBLEM_CELL = 24

const HUES = ['red', 'green', 'yellow', 'blue', 'purple', 'aqua', 'orange'] as const
// Orange and red mean attention and error on the launcher, so they only ever
// accent an emblem and never lead one.
const LEADS = ['green', 'yellow', 'blue', 'purple', 'aqua'] as const
const SHAPES: EmblemShape[] = ['quarter', 'half', 'circle', 'ring', 'triangle', 'leaf', 'bars']

const bright = (hue: string) => `var(--gruvbox-${hue})`
const neutral = (hue: string) => `var(--gruvbox-neutral-${hue})`
const CREAM = 'var(--gruvbox-fg1)'
const DEEP = 'var(--gruvbox-bg1)'

// FNV-1a, then mulberry32 seeded with it: small, fast and stable across
// browsers, which is all an emblem needs.
function hash(text: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return h >>> 0
}

function random(seed: number): () => number {
  let a = seed
  return () => {
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export function projectEmblem(name: string): Emblem {
  const next = random(hash(name.trim().toLowerCase()))
  const pick = <T>(items: readonly T[]): T => items[Math.floor(next() * items.length)]

  const primary = pick(LEADS)
  const others = HUES.filter((h) => h !== primary)
  const secondary = pick(others)
  const tertiary = pick(others.filter((h) => h !== secondary))

  // The primary colour is listed three times so it carries the tile; cream
  // and the deep background keep the brights from fighting.
  const colours = [bright(primary), bright(primary), bright(primary), bright(secondary), neutral(tertiary), CREAM, DEEP]

  const cells: EmblemCell[] = []
  for (let y = 0; y < 2; y++) {
    for (let x = 0; x < 2; x++) {
      const background = pick(colours)
      let foreground = pick(colours)
      while (foreground === background) foreground = pick(colours)
      cells.push({
        x: x * EMBLEM_CELL,
        y: y * EMBLEM_CELL,
        shape: pick(SHAPES),
        rotation: Math.floor(next() * 4),
        background,
        foreground,
      })
    }
  }

  // At least one cell shows the primary colour, so the card's tint matches.
  if (!cells.some((c) => c.background === bright(primary) || c.foreground === bright(primary))) {
    cells[Math.floor(next() * cells.length)].foreground = bright(primary)
  }

  return { hue: bright(primary), cells }
}
