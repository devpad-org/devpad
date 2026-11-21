import { Marked, marked } from 'marked'
import DOMPurify from 'dompurify'

marked.use({
  breaks: true,
  gfm: true,
})

function escapeHtml(text: string): string {
  return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

// literal renders raw HTML as the text it is, so nothing in it is hidden:
// an HTML comment, or an element styled out of sight, shows as written.
// So does a link reference definition, which Markdown otherwise renders as
// nothing, making `[//]: # (...)` a comment. Text an agent wrote for other
// agents to read is shown this way, so the user sees all that they read.
const literal = new Marked({ breaks: true, gfm: true })
literal.use({
  // Definitions can sit in quotes and list items too, which walkTokens
  // reaches. Links that use one still resolve: the lexer has kept it.
  walkTokens(token) {
    if (token.type === 'def') Object.assign(token, { type: 'html', block: true, pre: false, text: token.raw })
  },
  renderer: {
    html({ text, block }) {
      return block ? `<p>${escapeHtml(text)}</p>` : escapeHtml(text)
    },
  },
})

const emojiMap: Record<string, string> = {
  ':rocket:': '🚀', ':white_check_mark:': '✅', ':x:': '❌', ':warning:': '⚠️',
  ':bulb:': '💡', ':gear:': '⚙️', ':file_folder:': '📁', ':memo:': '📝',
  ':sparkles:': '✨', ':tada:': '🎉', ':wrench:': '🔧', ':bug:': '🐛',
  ':zap:': '⚡', ':fire:': '🔥', ':thumbsup:': '👍', ':thumbsdown:': '👎',
  ':eyes:': '👀', ':heavy_check_mark:': '✅', ':arrow_right:': '➡️', ':star:': '⭐',
  ':package:': '📦', ':lock:': '🔒', ':key:': '🔑', ':hammer:': '🔨',
  ':link:': '🔗', ':clipboard:': '📋', ':mag:': '🔍', ':pencil:': '✏️',
  ':green_circle:': '🟢', ':red_circle:': '🔴', ':check:': '✅', ':x_mark:': '❌',
}

// Rendered text loads no images but devpad's: an image's address can carry
// anything the page shows to whoever serves it, so text an agent wrote
// could otherwise send it out of the user's browser. Everything else that
// fetches by itself goes: media, SVG images, picture sources, and styles,
// which can fetch a background.
const PURIFY_CONFIG = {
  FORBID_TAGS: ['style', 'audio', 'video', 'source', 'track', 'picture', 'input', 'image', 'feImage', 'use'],
  FORBID_ATTR: ['style', 'background', 'poster', 'srcset'],
  RETURN_DOM_FRAGMENT: true as const,
}

// webAddress is src resolved against the page when it is an http(s)
// address, or null.
function webAddress(src: string): URL | null {
  try {
    const url = new URL(src, window.location.href)
    return url.protocol === 'https:' || url.protocol === 'http:' ? url : null
  } catch {
    return null
  }
}

// note is text Markdown would otherwise leave unseen, shown beside what it
// belongs to.
function note(doc: Document, text: string): HTMLElement {
  const span = doc.createElement('span')
  span.className = 'md-note'
  span.textContent = ` (${text})`
  return span
}

// finish serializes sanitized markup, with each image that is not devpad's
// made a link to it, or text when it has no web address. With literal, an
// image's description and a link's title, which only show on hover or when
// an image fails, show as text. The fragment belongs to DOMPurify's inert
// document, where no image loads, and nothing here moves it into the page's.
function finish(fragment: DocumentFragment, literal: boolean): string {
  const doc = fragment.ownerDocument
  const box = doc.createElement('div')
  box.append(fragment)
  box.querySelectorAll('img').forEach((img) => {
    const src = img.getAttribute('src') ?? ''
    const alt = img.getAttribute('alt') ?? ''
    const title = img.getAttribute('title') ?? ''
    const url = webAddress(src)
    if (url?.origin === window.location.origin) {
      if (literal) {
        img.removeAttribute('title')
        img.after(...[alt, title].filter(Boolean).map((text) => note(doc, text)))
      }
      return
    }
    const stand = doc.createElement(url ? 'a' : 'span')
    if (url) {
      stand.setAttribute('href', src)
      stand.setAttribute('rel', 'noopener noreferrer')
    }
    if (title) stand.setAttribute('title', title)
    stand.textContent = `Image: ${alt || (url ? src : 'not shown')}`
    img.replaceWith(stand)
  })
  if (literal) {
    box.querySelectorAll('[title]').forEach((el) => {
      const title = el.getAttribute('title') ?? ''
      el.removeAttribute('title')
      if (title) el.after(note(doc, title))
    })
  }
  return box.innerHTML
}

// renderMarkdown renders Markdown as sanitized HTML that loads no image but
// devpad's. With literalHtml, raw HTML in it is shown as text instead of
// rendered, and so is what an image or link says only on hover.
export function renderMarkdown(content: string, literalHtml = false): string {
  const withEmoji = content.replace(/:[a-z_]+:/g, (m) => emojiMap[m] || m)
  const raw = (literalHtml ? literal : marked).parse(withEmoji) as string
  return finish(DOMPurify.sanitize(raw, PURIFY_CONFIG), literalHtml)
}
