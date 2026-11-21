<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { renderMarkdown } from '@/utils/markdown'
import { writeClipboardText } from '@/utils/clipboard'

// literalHtml shows raw HTML in the content as text, so nothing in it is
// hidden from the reader.
const props = defineProps<{ content: string; literalHtml?: boolean }>()
const html = computed(() => renderMarkdown(props.content, props.literalHtml))
const root = ref<HTMLElement | null>(null)

const ICON_ATTRS = 'width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"'
const ICONS = {
  idle: `<svg ${ICON_ATTRS} stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`,
  copied: `<svg ${ICON_ATTRS} stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg>`,
  failed: `<svg ${ICON_ATTRS} stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>`,
}
const LABELS = { idle: 'Copy code', copied: 'Copied', failed: 'Copy failed' }
type CopyState = keyof typeof ICONS

const timers = new Set<number>()

onUnmounted(() => {
  timers.forEach((t) => clearTimeout(t))
  timers.clear()
})

function setState(button: HTMLButtonElement, state: CopyState): void {
  button.classList.toggle('copied', state === 'copied')
  button.classList.toggle('failed', state === 'failed')
  button.title = LABELS[state]
  button.setAttribute('aria-label', LABELS[state])
  button.innerHTML = ICONS[state]
}

// v-html replaces the rendered markup whenever the content changes (on every
// streamed chunk), so the buttons are added again after each render.
function addCopyButtons(): void {
  root.value?.querySelectorAll('pre').forEach((pre) => {
    if (pre.parentElement?.classList.contains('code-block')) return
    const wrapper = document.createElement('div')
    wrapper.className = 'code-block'
    const button = document.createElement('button')
    button.type = 'button'
    button.className = 'code-copy-btn'
    setState(button, 'idle')
    pre.replaceWith(wrapper)
    wrapper.append(pre, button)
  })
}

onMounted(addCopyButtons)
watch(html, addCopyButtons, { flush: 'post' })

async function onClick(event: MouseEvent): Promise<void> {
  const button = (event.target as Element | null)?.closest<HTMLButtonElement>('.code-copy-btn')
  if (!button) return
  const pre = button.parentElement?.querySelector('pre')
  if (!pre) return
  // marked ends every code block with a newline; leave it off so a copied
  // command doesn't run the moment it is pasted into a terminal.
  const text = (pre.textContent ?? '').replace(/\n$/, '')

  let state: CopyState
  try {
    await writeClipboardText(text)
    state = 'copied'
  } catch (err) {
    console.error('Failed to copy code block:', err)
    state = 'failed'
  }
  setState(button, state)
  const timer = window.setTimeout(() => {
    timers.delete(timer)
    if (button.isConnected) setState(button, 'idle')
  }, state === 'copied' ? 2000 : 3000)
  timers.add(timer)
}
</script>

<template>
  <div ref="root" class="msg-text markdown-body" @click="onClick" v-html="html" />
</template>

<style scoped>
/* What an image or link says only on hover, shown beside it as text. */
.markdown-body :deep(.md-note) {
  color: var(--text-secondary);
}

.markdown-body :deep(.code-block) {
  position: relative;
}

.markdown-body :deep(.code-copy-btn) {
  position: absolute;
  top: 5px;
  right: 5px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-raised);
  color: var(--text-tertiary);
  cursor: pointer;
  opacity: 0;
  transition: opacity var(--transition-fast), border-color var(--transition-fast), background var(--transition-fast), color var(--transition-fast);
}

.markdown-body :deep(.code-block:hover .code-copy-btn),
.markdown-body :deep(.code-copy-btn:focus-visible),
.markdown-body :deep(.code-copy-btn.copied),
.markdown-body :deep(.code-copy-btn.failed) {
  opacity: 1;
}

.markdown-body :deep(.code-copy-btn:hover) {
  border-color: var(--border-strong);
  background: var(--bg-hover);
  color: var(--text-primary);
}

.markdown-body :deep(.code-copy-btn.copied) {
  border-color: var(--success-border);
  background: var(--success-bg);
  color: var(--success);
}

.markdown-body :deep(.code-copy-btn.failed) {
  border-color: var(--error-border);
  background: var(--error-bg);
  color: var(--error);
}

@media (hover: none) {
  .markdown-body :deep(.code-copy-btn) {
    opacity: 1;
  }
}
</style>
