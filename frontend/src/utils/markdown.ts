import { marked } from 'marked'
import DOMPurify from 'dompurify'

marked.use({
  breaks: true,
  gfm: true,
})

const emojiMap: Record<string, string> = {
  ':rocket:': '🚀', ':white_check_mark:': '✅', ':x:': '❌', ':warning:': '⚠️',
  ':bulb:': '💡', ':gear:': '⚙️', ':file_folder:': '📁', ':memo:': '📝',
  ':sparkles:': '✨', ':tada:': '🎉', ':wrench:': '🔧', ':bug:': '🐛',
  ':zap:': '⚡', ':fire:': '🔥', ':thumbsup:': '👍', ':thumbsdown:': '👎',
  ':eyes:': '👀', ':heavy_check_mark:': '✔️', ':arrow_right:': '➡️', ':star:': '⭐',
  ':package:': '📦', ':lock:': '🔒', ':key:': '🔑', ':hammer:': '🔨',
  ':link:': '🔗', ':clipboard:': '📋', ':mag:': '🔍', ':pencil:': '✏️',
  ':green_circle:': '🟢', ':red_circle:': '🔴', ':check:': '✅', ':x_mark:': '❌',
}

export function renderMarkdown(content: string): string {
  const withEmoji = content.replace(/:[a-z_]+:/g, (m) => emojiMap[m] || m)
  const raw = marked.parse(withEmoji) as string
  return DOMPurify.sanitize(raw)
}
