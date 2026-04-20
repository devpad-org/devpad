# Devpad Design Language

A reference for implementing Devpad's UI. This document defines the tokens, principles, and component patterns that together make up the visual system. Treat it as the source of truth — if the code disagrees with this document, the code is wrong.

---

## Contents

1. [Principles](#principles)
2. [Color tokens](#color-tokens)
3. [Typography](#typography)
4. [Spacing & layout](#spacing--layout)
5. [Borders, radii, elevation](#borders-radii-elevation)
6. [Motion](#motion)
7. [Iconography](#iconography)
8. [Components](#components)
9. [Syntax highlighting](#syntax-highlighting)
10. [Accessibility](#accessibility)
11. [Anti-patterns](#anti-patterns)

---

## Principles

Five commitments that override individual preference. When in doubt, return to these.

**1. Monochrome with one accent.** The interface is built on a single neutral ramp from near-black to near-white. Exactly one saturated color (cyan) carries semantic weight, used sparingly to mark state, focus, or the agent's voice. Never introduce a second accent. If you feel the need to, you are solving the wrong problem.

**2. Depth through layering, not decoration.** Surfaces stack in four levels (void → base → elevated → raised). Depth comes from background steps and hairline borders, never from drop shadows, gradients on panels, or blur. A single 1px top-inner-highlight on the outer frame is the only "glass" effect permitted.

**3. Density with breathing room.** We are an IDE. We show a lot of information. But every element earns its space — no padding below the minimum, no padding above what the content needs. Aim for information density closer to Linear than to Notion.

**4. Sentence case, always.** No Title Case. No ALL CAPS, except for section headers at 10px with tracked letter-spacing (a distinct semantic, not a style choice). This is a rule, not a suggestion.

**5. The editor is a reading surface.** Code is the primary content. Chrome exists to serve it. Anything in the chrome that competes with the code for attention is a bug — including syntax highlighting that's too colorful, sidebars that are too bright, and agent panels that pulse or animate without cause.

---

## Color tokens

All colors live as CSS custom properties in a single `:root` block. Never use raw hex values outside that block.

### Surfaces

| Token | Value | Use |
|---|---|---|
| `--bg-void` | `#08090c` | Editor background, deepest layer |
| `--bg-base` | `#0b0d11` | App background, terminal, activity rail |
| `--bg-elevated` | `#0f1116` | Sidebars, agent panel, tab strip, top bar |
| `--bg-raised` | `#14161c` | Chips, tags, input fields on elevated surfaces |
| `--bg-hover` | `rgba(255, 255, 255, 0.035)` | Hover state for rows, buttons |
| `--bg-selected` | `rgba(255, 255, 255, 0.06)` | Active/selected state for rows |

Surfaces must always step in one direction — a raised element never sits on a void background, a void element never sits on a raised one. When you need a recessed feel (like the editor behind the tab strip), use void. When you need something to float (like a chip), use raised.

### Borders

| Token | Value | Use |
|---|---|---|
| `--border-hairline` | `rgba(255, 255, 255, 0.04)` | Default panel-to-panel seams |
| `--border-subtle` | `rgba(255, 255, 255, 0.06)` | Component borders (inputs, cards) |
| `--border-strong` | `rgba(255, 255, 255, 0.1)` | Hover borders, emphasized edges |
| `--border-top-highlight` | `rgba(255, 255, 255, 0.05)` | Inner top highlight on outer frame |

All borders are `0.5px` except the top highlight, which is `1px` inset. Never go thicker than 1px — the design breaks if you do.

### Text

| Token | Value | Use |
|---|---|---|
| `--text-primary` | `#e8eaed` | Body text, active states, code |
| `--text-secondary` | `#9499a2` | Labels, muted body text, inactive tabs |
| `--text-tertiary` | `#5f646e` | Section headers, metadata, icons |
| `--text-dim` | `#40454e` | Line numbers, disabled, punctuation |

Four levels. Never introduce a fifth. If something doesn't fit cleanly into one of these, step back and reconsider what you're trying to say.

### Accent

| Token | Value | Use |
|---|---|---|
| `--accent` | `#4dd0e1` | The cyan. The only chromatic color in the UI. |
| `--accent-dim` | `#2a7a84` | Minimap marks, de-emphasized accent use |
| `--accent-glow` | `rgba(77, 208, 225, 0.12)` | Focus ring fills, active row backgrounds |
| `--accent-border` | `rgba(77, 208, 225, 0.25)` | Focus ring borders, agent message borders |

The accent appears in exactly these approved locations:

- Active tab top border (1.5px)
- Active file / active rail item left border (2px)
- Agent message left border (1.5px)
- Send button fill
- Status dot when agent is working
- Focus ring on inputs
- Minimap section markers
- Terminal cursor and command prompt
- Syntax highlighting of the single "highlighted line" state

This is an exhaustive list. If you find yourself wanting the accent somewhere else, the answer is almost certainly no.

### Semantic

| Token | Value | Use |
|---|---|---|
| `--success` | `#6fbf73` | Running status, successful tool call checkmarks, HTTP 2xx |
| `--warning` | `#d4a24c` | Pending state, HTTP 4xx |

Semantic colors are not accents. They encode meaning (run state, build status) and must only appear where meaning demands it. Never decorative.

---

## Typography

### Font families

```css
--font-sans: 'Geist', -apple-system, BlinkMacSystemFont, sans-serif;
--font-mono: 'Geist Mono', 'JetBrains Mono', Menlo, monospace;
```

Two families. That's it. Geist for UI chrome, Geist Mono for code, file names, technical strings (commit hashes, model names, port numbers, HTTP codes). Do not load a third family.

Enable `font-feature-settings: "ss01", "cv11"` on the body — Geist's stylistic sets make numerals and the `0` character more distinctive in code contexts.

### Size scale

| Purpose | Size | Weight | Notes |
|---|---|---|---|
| Section header (uppercase) | 10px | 600 | `letter-spacing: 0.09em; text-transform: uppercase` |
| Metadata, tags, chips | 10.5px | 500 | Often mono |
| Status, small labels | 11px | 500 | |
| Tool call rows, change items | 11.5px | 400–500 | |
| Body UI (tabs, tree items, buttons) | 12px–12.5px | 400–500 | |
| Code | 12px | 400 | `line-height: 1.7` |
| Project name, active elements | 13px | 500 | |
| Body input text | 12.5px–13px | 400 | |

No text below 10px. No text above 13px anywhere in the chrome. The IDE is a utility, not an editorial layout.

### Weight rules

- **400** for most body text and metadata.
- **500** for active states, file names in selected rows, project names, primary buttons.
- **600** only for uppercase section headers.
- **Never 700.** If you need more emphasis than 500 provides, switch to primary text color, not heavier weight.

### Line heights

- Body UI: `1.5`
- Code: `1.7`
- Dense lists (tool rows, tree items): `1.5` with 5px vertical padding

### Letter spacing

- Default: 0 (Geist is tight enough)
- Section headers (uppercase): `0.09em`
- Tags in mono: `0`
- Large project names / display: `-0.005em` (subtle optical correction only)

### Case

Sentence case everywhere except the uppercase section headers (`EXPLORER`, `AGENT`, `SUMMARY`, `TERMINAL`). These are deliberately different — they function as structural landmarks, not content. Never use Title Case.

---

## Spacing & layout

### Scale

Use multiples of 2px for internal component spacing, multiples of 4px for component-to-component, and rem-based values for rhythm between major sections.

| Size | Value | Typical use |
|---|---|---|
| `xs` | 2px | Icon gaps, tight stacks |
| `sm` | 4px | Action button clusters |
| `md` | 6px | Tool row internal padding |
| `base` | 8px | Default gap, padding for dense rows |
| `lg` | 10px | Agent input padding, card internal |
| `xl` | 12px | Panel padding, section breaks |
| `2xl` | 14px | Sidebar padding |
| `3xl` | 16px | Major panel padding, top bar horizontal |
| `4xl` | 24px | Section dividers |

### Panel widths

- Activity rail: `44px` (fixed)
- File sidebar: `220px` (resizable, default)
- Agent panel: `320px` (resizable, default)
- Editor: `1fr` (fills)
- Minimap: `200px` (fixed, toggleable)

### Heights

- Top bar: `40px`
- Tab strip: `36px`
- Terminal header: fixed at `~36px` total padding
- Terminal body: `max-height: 160px`, resizable

### Padding conventions

- Icon buttons: `28×28px` with icon at `14px`
- Row items (tree, tool calls, changes): `5px 8px`
- Sidebar section headers: `14px` horizontal, `12px` bottom
- Agent message quote-block: `4px 8px 4px 12px`

---

## Borders, radii, elevation

### Border width

- **Default: `0.5px`.** This is the standard for all panel seams, component borders, and dividers.
- **Accent indicators: `1.5–2px`.** The cyan tab top-border is `1.5px`; the cyan left-border on active files/rails is `2px`. These are the only places thicker lines appear.
- **Never 1px solid** for panel seams — it looks heavy against the dark palette. Always 0.5px.

### Border radius

| Token | Value | Use |
|---|---|---|
| `radius-sm` | 3px | Tags, pills inside tool rows |
| `radius-md` | 4–5px | Icon buttons, rail items, tool rows, row highlights |
| `radius-lg` | 6–8px | Inputs, agent composer, send button, chips |
| `radius-xl` | 10–12px | Outer IDE frame, major containers |

Never round the outer edge of panels that butt against each other — the seam is straight and hairline-bordered. Only the outer frame of the whole IDE gets the 12px radius.

### Elevation

There are no drop shadows. There is exactly one shadow effect in the system, applied only to the outer IDE frame:

```css
box-shadow:
  0 1px 0 var(--border-top-highlight) inset,
  0 40px 80px rgba(0, 0, 0, 0.6),
  0 0 0 1px rgba(0, 0, 0, 0.4);
```

The inset top highlight is what sells the "premium glass" feel. The outer shadow only applies when the IDE is rendered in a floating context (settings modal, marketing site, onboarding). In-app, the IDE fills the viewport and has no shadow.

---

## Motion

Motion is restrained. The goal is responsiveness — a user should feel the interface reacting, not watch it perform.

### Duration

- **Micro-interactions** (hover, focus, button press): `150ms`
- **State changes** (panel expand, menu open): `200ms`
- **Semantic feedback** (success checkmark, pulse): `1.4s–2.4s` slow cycles

### Easing

Default to `ease` for most transitions. Reserve `cubic-bezier(0.4, 0, 0.2, 1)` for more intentional state changes. Never use bouncy easings.

### Approved animations

- **Status pulse** — `pulse` at 2.4s on the running status dot. Opacity + scale only, no color change.
- **Agent thinking dot** — `blink` at 1.4s, single dot, opacity-only.
- **Terminal cursor** — step-end blink at 1s, pure binary on/off.
- **Hover transitions** — background and color only, 150ms ease.
- **Focus ring appearance** — box-shadow fade-in at 150ms.

### Forbidden

No scroll-triggered animations. No entrance animations on mount (the IDE should feel instantly present). No parallax. No particles. No gradients that animate. No morph transitions between states. No "shimmer" loading states — use the thinking dot pattern instead.

---

## Iconography

### Library

Use [Lucide](https://lucide.dev) or [Phosphor](https://phosphor-icons.com) (thin variant). Both are stroke-based, 24x24 source, and render consistently at small sizes.

### Sizing

| Context | Size | Stroke |
|---|---|---|
| Activity rail | `16px` | `1.5px` |
| Sidebar actions, inline chrome | `12px` | `1.5px` |
| Top bar icon buttons | `14px` | `1.5px` |
| Tool row checkmarks | `12px` | `2.5px` (intentionally heavier — this is a semantic mark, not chrome) |
| Send button | `12px` | `2.25px` |
| File tree type indicators | `12px` | `1.5px` or mono character fallback (`M`, `</>`, `.js`) |

### Rules

- Always use `fill="none"` with `stroke="currentColor"` so icons inherit text color.
- Never fill icons. The only filled element is the agent logo square, which is a brand mark, not an icon.
- Render at exact pixel sizes on a pixel grid — fractional sizes cause blur.
- Use mono-character substitutes (`M`, `</>`) for file type indicators when they'd be clearer than an icon. These render in `--font-mono` at 10px.

---

## Components

### Top bar

- Height `40px`, `--bg-elevated`, hairline bottom border.
- Left cluster: back button (`14px` chevron) + project name (`13px/500`) + status lockup.
- Right cluster: icon buttons (`28×28px`), gap `4px`.
- Status lockup: 5px pulsing dot + label (`11px/500, --text-tertiary`).

### Activity rail

- Width `44px`, `--bg-base`, hairline right border.
- Items are `32×32px` with `6px` radius, `16px` icons at `1.5px` stroke.
- Default color `--text-tertiary`; hover `--text-secondary` + `--bg-hover`; active `--text-primary` + `--bg-selected` + `2px` accent left border at `-10px` offset (so it sits flush with the panel seam).

### Sidebar / file tree

- Width `220px`, `--bg-elevated`, hairline right border.
- Header: uppercase section title + action icons, padding `14px horizontal, 12px bottom`.
- File items: `5px 8px` padding, `5px` radius, icon + label at `12.5px`.
- Active file: `--bg-selected` background, `--text-primary` color, `500` weight, 2px accent left border starting `6px` from top of row.

### Tabs

- Height `36px`, tab strip on `--bg-elevated`, editor on `--bg-void`.
- Inactive tab: `--text-secondary`, `400` weight, hover brightens to `--text-primary`.
- Active tab: `--bg-void` background (matches editor), `--text-primary`, `500` weight, **1.5px solid accent top border**.
- File type indicator in mono at 10px, accent-colored on active tab.
- Close button: `10px` X, opacity 0 by default, 0.6 on hover or when tab is active.

### Editor

- Background `--bg-void`.
- Line numbers: `44px` gutter, right-aligned, `--text-dim`, `tabular-nums`, `user-select: none`.
- Code: `12px/1.7` mono, see [Syntax highlighting](#syntax-highlighting).
- Cursor line highlight: left border `2px` accent, background gradient `linear-gradient(90deg, rgba(77, 208, 225, 0.04) 0%, transparent 100%)`.

### Minimap

- Width `200px`, `--bg-void`, hairline left border.
- Lines are `2px` tall, `1px` margin. Widths vary by approximate line length (`85%`, `60%`, `35%`).
- Default color `--border-subtle` or `--border-strong` for medium-weight lines.
- Accent-dim for "important" or flagged lines (errors, current method, changed lines).
- Viewport indicator: subtle bordered rectangle at `rgba(255,255,255,0.02)` background.

### Agent panel

- Width `320px`, `--bg-elevated`, hairline left border.
- Header: agent logo square (`16×16px`, accent fill, dark letterform) + uppercase title.
- **Tool call rows** (the most important pattern): single row, `5px 8px` padding, `5px` radius. Layout: checkmark icon (`12px`, `--success`, `2.5px` stroke) + verb (`--text-primary`, `11.5px`) + target (mono, `10.5px`, `--text-secondary`) + count chip right-aligned (`--text-tertiary`, `10px`, mono). Tool calls that succeeded collapse to this single line — never expanded card UI for routine successes.
- **Agent messages**: quote-block style with `1.5px` accent left border, `--accent-glow` background, `4px 8px 4px 12px` padding, `0 4px 4px 0` radius. Body text `12px/1.55`, `--text-primary`.
- **Summary lists**: uppercase section header followed by rows. Each row: `4px` accent dot + label (`--text-primary`) + status tag right-aligned (tag chip on `--bg-raised` with subtle border).
- **Thinking state**: single row with blinking accent dot + muted description text.
- **Composer**: `--bg-base` input with subtle border, `8px` radius, `10px` padding. Contains input field, model chip, send button. Focus ring: `0 0 0 3px --accent-glow` + accent-bordered.

### Composer / agent input

- Input wrapper: `--bg-base`, 0.5px subtle border, 8px radius, 1px inset top highlight, 1px bottom shadow.
- Model chip: `--bg-raised` with subtle border, `2px 7px` padding, 4px radius, mono 10.5px, 4px accent dot on left.
- Send button: `26×26px`, 5px radius, solid accent fill, `--bg-base` icon color, `translateY(-1px)` on hover.
- Focus: remove default outline; show `3px` accent glow ring via box-shadow.

### Terminal

- `--bg-base` background, hairline top border.
- Header: uppercase title + connection status dot.
- Body: `11.5px` mono, `1.65` line height.
- Prompt: `--accent` color for the `$`, `--text-primary` for username, `--text-secondary` for host/`@`, `--text-secondary` for path.
- HTTP status: `--success` for 2xx, `--warning` for 4xx, `--text-tertiary` for the rest.
- Cursor: `7×14px` solid accent block, step-end blink at 1s.

### Buttons (general)

- Icon-only default: `28×28px`, transparent background, `--text-tertiary` → `--text-secondary` on hover, `5px` radius, `--bg-hover` on hover.
- Primary action: solid accent fill, `--bg-base` icon/text, never used for more than one button per panel. The send button is the canonical example.
- There are no outlined or "secondary" buttons in the system. If you need secondary emphasis, use an icon button.

### Chips / tags

- Pill-shaped: `radius-md` (4–5px), never fully pill (20px+).
- Mono font for technical values (model names, versions, counts).
- `--bg-raised` background on `--bg-elevated` surfaces; `--bg-elevated` background on `--bg-base` surfaces. Always one step up from their container.
- 0.5px subtle border, 1–2px vertical padding, 6–8px horizontal.

---

## Syntax highlighting

Syntax highlighting is **nearly monochromatic by design**. Colorful highlighting competes with chrome and agent state for attention — we keep it restrained so when we do use color (the accent on a highlighted line), it actually means something.

### Palette

| Token | Value | Applied to |
|---|---|---|
| `--syn-comment` | `#5f646e` | Comments, doctypes |
| `--syn-keyword` | `#c8cbd1` | Keywords, function names, default code |
| `--syn-string` | `#9499a2` | String literals |
| `--syn-tag` | `#b4b8c0` | HTML/JSX tags |
| `--syn-attr` | `#7a7f88` | HTML attributes, CSS properties |
| `--syn-value` | `#e8eaed` | CSS values, numeric literals, emphasized tokens |
| `--syn-punct` | `#40454e` | Brackets, semicolons, operators |
| `--syn-accent` | `#4dd0e1` | **Only** on the currently highlighted/focused line |

Five neutral tones + one accent. Notice there is no purple, green, yellow, or red in code. This is deliberate and should not be changed.

### Rules

- Comments always italic. Nothing else is italic.
- String literals and attributes are dimmer than keywords — this is inverted from most themes and is intentional. Strings are data; keywords are structure. We emphasize structure.
- Punctuation is the dimmest readable value (`--text-dim`) so the eye skims past it.
- The accent color (`--syn-accent`) appears on exactly one token per visible region at a time: the value on the current cursor line, or a flagged identifier in a diff view. Never more than one token per line.

### Error / warning states

- Error underline: 1px wavy, `--warning` at the error token. Never red — too loud against the palette.
- Squiggles for warnings: 1px dotted, `--accent-dim`.
- Never highlight the full line for errors. The squiggle + gutter mark is enough.

---

## Accessibility

### Contrast

Our palette is built around WCAG AA at minimum, AAA where feasible.

- `--text-primary` (`#e8eaed`) on `--bg-void` (`#08090c`): **~17:1** ✓ AAA
- `--text-secondary` (`#9499a2`) on `--bg-void`: **~7.2:1** ✓ AAA
- `--text-tertiary` (`#5f646e`) on `--bg-void`: **~3.9:1** ✓ AA (large text only — only used for 11px+ labels in headers, not body)
- `--text-dim` (`#40454e`) on `--bg-void`: **~2.4:1** — **only used for line numbers and punctuation**, which are never conveying critical information. If you find yourself using `--text-dim` for anything a user needs to read, it's wrong.

### Focus

- Every interactive element has a visible focus state, not just `:hover`.
- Default focus style: `box-shadow: 0 0 0 3px --accent-glow` + `border-color: --accent-border`.
- Never remove `outline` without replacing it with an equivalent `box-shadow` ring.
- Tab order must match visual order. The order in the DOM matters.

### Motion

- Wrap all non-essential animation in `@media (prefers-reduced-motion: reduce)` — set `animation: none` and `transition-duration: 0.01ms` inside.
- The only animations that should continue to run with reduced motion: the terminal cursor blink (it's a critical state indicator) and the success checkmark appearance (instantaneous anyway).

### Semantics

- Use actual `<button>` for buttons, not divs.
- File tree is an ARIA `tree` with `treeitem` rows, `aria-selected` on the active item.
- Tabs use `role="tablist"` / `role="tab"` / `aria-selected`.
- The agent panel's message stream uses `role="log"` with `aria-live="polite"` so screen readers announce new tool calls and messages.
- Icon-only buttons always have `aria-label` or a visible label via `title`.

### Color independence

No UI state is communicated by color alone:

- Active tab: accent top border **and** background shift **and** weight change.
- Running status: colored dot **and** "Running" text label.
- Tool success: green checkmark **and** the mere presence of the row (vs. a spinning state).

If a colorblind user or a monochrome display still conveys the state, we're good.

---

## Anti-patterns

Things that look close to correct but are wrong. Call these out in code review.

**❌ Using the accent for decoration.** Cyan for a divider, a hover state on a regular button, a non-semantic icon highlight. Accent carries meaning — using it decoratively dilutes that meaning everywhere else.

**❌ Introducing a second accent.** Purple for "agent", green for "running", orange for "warning state", etc. The system already uses `--success` and `--warning` for semantic state; don't elevate either to accent status.

**❌ Raised backgrounds on outer panels.** Panels are `--bg-elevated` at most. `--bg-raised` is for components *on* panels (chips, inputs). A sidebar on `--bg-raised` breaks the depth model.

**❌ Drop shadows on internal components.** Only the outermost frame has a shadow. A shadow on a card inside the agent panel will look cheap and break the flat-layer model.

**❌ Heavier-than-0.5px borders for panels.** 1px reads as a heavy rule; we use surface shifts + hairlines for separation. 1px is reserved for the top highlight and for accent indicators only.

**❌ 600 or 700 weight on body text.** We use 400 and 500. Emphasis comes from color contrast (primary vs. secondary text), not weight.

**❌ Title Case anywhere.** "New File" is wrong. "New file" is correct. "EXPLORER" is correct (it's an uppercase section header, a distinct semantic).

**❌ A third font family.** Geist + Geist Mono is the entire type system. No display face. No third UI face.

**❌ Cards with rounded corners inside dense lists.** Tool call rows, tree items, and change items have `5px` radius because they are discrete interactive rows. Don't wrap them in further cards with more radius — the nesting breaks the tight rhythm.

**❌ Animations on mount.** The IDE appears instantly. No fade-in, no slide-up. A user opening the app wants to start working, not watch a reveal.

**❌ Colorful syntax highlighting.** If you find yourself adding purple for keywords or green for strings, you are backing away from the design. The neutral palette + single accent is load-bearing for the whole aesthetic.

**❌ Expanded tool call UI for successes.** A successful `edit` shouldn't show a card with file path, line range, and diff preview — it collapses to a one-line row. Expansion is for *failures* and *in-progress* states, or when the user explicitly requests detail.

---

## Versioning

When updating this document, bump the version and note the change:

- **v1.0** (2026-04) — Initial design language. Monochromatic palette, Geist typography, single-accent system.

Changes to tokens (colors, sizes, fonts) must be proposed via PR against this file, with reasoning and at least one mockup showing the before/after. The design language is not open for drift.