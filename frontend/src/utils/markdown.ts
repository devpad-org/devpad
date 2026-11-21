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

// renderMarkdown renders Markdown as sanitized HTML. With literalHtml, raw
// HTML in it is shown as text instead of rendered.
export function renderMarkdown(content: string, literalHtml = false): string {
  const withEmoji = content.replace(/:[a-z_]+:/g, (m) => emojiMap[m] || m)
  const raw = (literalHtml ? literal : marked).parse(withEmoji) as string
  return DOMPurify.sanitize(raw)
}
