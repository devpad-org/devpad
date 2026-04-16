<script setup lang="ts">
import { ref, computed, nextTick, onMounted } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { aiApi, type AIModel, type ChatMessage, type StreamEvent } from '@/api/ai'

marked.setOptions({
  breaks: true,
  gfm: true,
})

function renderMarkdown(content: string): string {
  const raw = marked.parse(content) as string
  return DOMPurify.sanitize(raw)
}

const props = defineProps<{
  workspaceId: number
}>()

interface ToolUsage {
  name: string
  args: string
  result?: string
}

interface DisplayMessage {
  role: 'user' | 'assistant'
  content: string
  toolUsages?: ToolUsage[]
}

const messages = ref<DisplayMessage[]>([])
const inputValue = ref('')
const chatBody = ref<HTMLElement | null>(null)
const models = ref<AIModel[]>([])
const selectedModel = ref('')
const streaming = ref(false)
const abortController = ref<AbortController | null>(null)
const inputFocused = ref(false)
const inputEl = ref<HTMLTextAreaElement | null>(null)

function autoResize() {
  const el = inputEl.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 120) + 'px'
}

onMounted(async () => {
  try {
    const res = await aiApi.listModels()
    models.value = res.models.filter((m) => m.configured)
    if (models.value.length > 0) {
      selectedModel.value = models.value[0].id
    }
  } catch {
    // models will remain empty
  }
})

function formatToolArgs(args: string): string {
  try {
    const parsed = JSON.parse(args)
    return Object.entries(parsed)
      .map(([k, v]) => `${k}: ${typeof v === 'string' && v.length > 80 ? v.slice(0, 80) + '…' : v}`)
      .join(', ')
  } catch {
    return args
  }
}

async function sendMessage() {
  const text = inputValue.value.trim()
  if (!text || streaming.value) return

  if (!selectedModel.value) {
    messages.value.push({
      role: 'assistant',
      content: 'No AI model is configured. Ask an admin to set up an AI provider in Settings.',
    })
    await nextTick()
    scrollToBottom()
    return
  }

  messages.value.push({ role: 'user', content: text })
  inputValue.value = ''

  // Add empty assistant message for streaming
  messages.value.push({ role: 'assistant', content: '', toolUsages: [] })
  const assistantIdx = messages.value.length - 1

  await nextTick()
  scrollToBottom()

  streaming.value = true
  const controller = new AbortController()
  abortController.value = controller

  // Build conversation history (exclude tool usages for the API)
  const chatMessages: ChatMessage[] = messages.value
    .slice(0, -1) // exclude the empty assistant message
    .map((m) => ({ role: m.role, content: m.content }))

  try {
    await aiApi.agentStream(
      selectedModel.value,
      chatMessages,
      props.workspaceId,
      (event: StreamEvent) => {
        if (event.error) {
          messages.value[assistantIdx].content += `\n\nError: ${event.error}`
        } else if (event.content) {
          messages.value[assistantIdx].content += event.content
        } else if (event.toolCalls) {
          // Tool is being called
          for (const tc of event.toolCalls) {
            const usage: ToolUsage = {
              name: tc.function.name,
              args: tc.function.arguments,
            }
            if (!messages.value[assistantIdx].toolUsages) {
              messages.value[assistantIdx].toolUsages = []
            }
            messages.value[assistantIdx].toolUsages!.push(usage)
          }
        } else if (event.toolResult) {
          // Tool result came back
          const usages = messages.value[assistantIdx].toolUsages
          if (usages) {
            const usage = usages.find((u) => u.name === event.toolResult!.name && !u.result)
            if (usage) {
              usage.result = event.toolResult.content
            }
          }
        }
        scrollToBottom()
      },
      controller.signal,
    )
  } catch (err: any) {
    if (err.name !== 'AbortError') {
      messages.value[assistantIdx].content =
        messages.value[assistantIdx].content || `Error: ${err.message}`
    }
  } finally {
    streaming.value = false
    abortController.value = null
    await nextTick()
    scrollToBottom()
  }
}

function stopStreaming() {
  abortController.value?.abort()
}

function newChat() {
  if (streaming.value) {
    abortController.value?.abort()
  }
  messages.value = []
  inputValue.value = ''
  if (inputEl.value) {
    inputEl.value.style.height = 'auto'
  }
}

const isThinking = computed(() => {
  if (!streaming.value) return false
  const last = messages.value[messages.value.length - 1]
  if (!last || last.role !== 'assistant') return false
  return !last.content && (!last.toolUsages || last.toolUsages.length === 0)
})

function scrollToBottom() {
  if (chatBody.value) {
    chatBody.value.scrollTop = chatBody.value.scrollHeight
  }
}
</script>

<template>
  <div class="agent-panel">
    <div class="agent-header">
      <div class="agent-header-info">
        <span class="agent-avatar">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 8V4H8" />
            <rect width="16" height="12" x="4" y="8" rx="2" />
            <path d="m2 14 6-6 6 6" />
            <path d="m14 8 4 4 4-4" />
          </svg>
        </span>
        <span class="agent-title">AI Agent</span>
      </div>
      <div class="agent-header-actions">
        <select v-if="models.length > 0" v-model="selectedModel" class="model-selector">
          <option v-for="m in models" :key="m.id" :value="m.id">{{ m.name }}</option>
        </select>
        <span v-else class="agent-badge">No Models</span>
        <button class="new-chat-btn" title="New Chat" @click="newChat">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 5v14" />
            <path d="M5 12h14" />
          </svg>
        </button>
      </div>
    </div>

    <div ref="chatBody" class="agent-body">
      <div v-if="messages.length === 0" class="chat-empty">
        <div class="empty-icon">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 8V4H8" />
            <rect width="16" height="12" x="4" y="8" rx="2" />
            <path d="m2 14 6-6 6 6" />
            <path d="m14 8 4 4 4-4" />
          </svg>
        </div>
        <p class="empty-text">Ask me anything about your code.</p>
      </div>
      <div
        v-for="(msg, i) in messages"
        :key="i"
        class="chat-message"
        :class="`msg-${msg.role}`"
      >
        <div class="msg-avatar">
          <template v-if="msg.role === 'assistant'">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 8V4H8" />
              <rect width="16" height="12" x="4" y="8" rx="2" />
              <path d="m2 14 6-6 6 6" />
              <path d="m14 8 4 4 4-4" />
            </svg>
          </template>
          <template v-else>
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2" />
              <circle cx="12" cy="7" r="4" />
            </svg>
          </template>
        </div>
        <div
          v-if="msg.role === 'assistant'"
          class="msg-content markdown-body"
        >
          <div v-if="msg.toolUsages && msg.toolUsages.length > 0" class="tool-usages">
            <div v-for="(tool, ti) in msg.toolUsages" :key="ti" class="tool-usage">
              <div class="tool-header">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
                </svg>
                <span class="tool-name">{{ tool.name }}</span>
                <span v-if="!tool.result && streaming" class="tool-spinner" />
                <span v-else-if="tool.result" class="tool-done">done</span>
              </div>
              <div class="tool-args">{{ formatToolArgs(tool.args) }}</div>
            </div>
          </div>
          <div v-if="msg.content" v-html="renderMarkdown(msg.content)" />
          <div v-if="i === messages.length - 1 && isThinking" class="thinking-indicator">
            <span class="thinking-dot" />
            <span class="thinking-dot" />
            <span class="thinking-dot" />
          </div>
        </div>
        <div v-else class="msg-content">{{ msg.content }}</div>
      </div>
    </div>

    <div class="agent-input-area">
        <div class="input-container" :class="{ focused: inputFocused }" @click="inputEl?.focus()">
        <textarea
          v-model="inputValue"
          class="agent-input"
          placeholder="Ask the AI agent…"
          rows="1"
          :disabled="streaming"
          @keydown.enter.exact.prevent="sendMessage"
          @focus="inputFocused = true"
          @blur="inputFocused = false"
          @input="autoResize"
          ref="inputEl"
        />
        <button v-if="streaming" class="agent-send agent-stop" @click="stopStreaming" title="Stop">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <rect x="6" y="6" width="12" height="12" rx="2" />
          </svg>
        </button>
        <button v-else class="agent-send" :class="{ active: inputValue.trim() }" @click="sendMessage" title="Send">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="m22 2-7 20-4-9-9-4Z" />
            <path d="M22 2 11 13" />
          </svg>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.agent-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.agent-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid var(--border-default);
  flex-shrink: 0;
}

.agent-header-info {
  display: flex;
  align-items: center;
  gap: 8px;
}

.agent-avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: var(--radius-md);
  background: linear-gradient(135deg, var(--accent-purple), var(--accent-blue));
  color: white;
}

.agent-title {
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--text-primary);
}

.agent-header-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2, 8px);
}

.agent-badge {
  font-size: 0.6rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  padding: 2px 8px;
  border-radius: 9999px;
  background: rgba(124, 58, 237, 0.15);
  color: var(--accent-purple);
}

.new-chat-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--text-secondary);
  border: 1px solid var(--border-default);
  cursor: pointer;
  transition: all var(--transition-fast);
}

.new-chat-btn:hover {
  background: var(--bg-surface-alt);
  color: var(--text-primary);
  border-color: var(--accent-purple);
}

.model-selector {
  font-size: 0.7rem;
  padding: 2px 8px;
  border-radius: var(--radius-md);
  background: var(--bg-surface-alt);
  border: 1px solid var(--border-default);
  color: var(--text-secondary);
  outline: none;
  cursor: pointer;
  transition: border-color var(--transition-fast);
}

.model-selector:focus {
  border-color: var(--accent-purple);
}

.chat-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  flex: 1;
  gap: var(--space-2);
  opacity: 0.5;
}

.empty-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  border-radius: var(--radius-lg);
  background: linear-gradient(135deg, var(--accent-purple), var(--accent-blue));
  color: white;
}

.empty-text {
  font-size: 0.75rem;
  color: var(--text-muted);
}

.agent-body {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-3);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.chat-message {
  display: flex;
  gap: var(--space-2);
  align-items: flex-start;
}

.msg-avatar {
  flex-shrink: 0;
  width: 24px;
  height: 24px;
  border-radius: var(--radius-md);
  display: flex;
  align-items: center;
  justify-content: center;
  margin-top: 2px;
}

.msg-assistant .msg-avatar {
  background: linear-gradient(135deg, var(--accent-purple), var(--accent-blue));
  color: white;
}

.msg-user .msg-avatar {
  background: var(--bg-surface-alt);
  color: var(--text-secondary);
}

.msg-content {
  flex: 1;
  font-size: 0.8rem;
  line-height: 1.55;
  color: var(--text-secondary);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-lg);
  min-width: 0;
}

.msg-assistant .msg-content {
  background: var(--bg-surface-alt);
}

.msg-user .msg-content {
  background: rgba(0, 212, 255, 0.06);
  color: var(--text-primary);
}

.agent-input-area {
  padding: var(--space-3);
  border-top: 1px solid var(--border-default);
  flex-shrink: 0;
}

.input-container {
  display: flex;
  align-items: flex-end;
  gap: var(--space-2);
  background: var(--bg-surface-alt);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-lg, 12px);
  padding: var(--space-2, 8px);
  padding-left: var(--space-3, 12px);
  transition: border-color var(--transition-fast), box-shadow var(--transition-fast);
  cursor: text;
}

.input-container.focused {
  border-color: var(--accent-purple);
  box-shadow: 0 0 0 2px rgba(124, 58, 237, 0.15);
}

.agent-input {
  flex: 1;
  background: transparent;
  border: none;
  font-family: var(--font-sans);
  font-size: 0.8rem;
  color: var(--text-primary);
  resize: none;
  outline: none;
  line-height: 1.5;
  max-height: 120px;
  min-height: 22px;
  padding: 4px 0;
}

.agent-input::placeholder {
  color: var(--text-muted);
}

.agent-send {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border-radius: var(--radius-md, 8px);
  background: rgba(124, 58, 237, 0.2);
  color: var(--text-muted);
  flex-shrink: 0;
  transition: all var(--transition-fast);
  cursor: pointer;
  border: none;
}

.agent-send.active {
  background: linear-gradient(135deg, var(--accent-purple), var(--accent-blue));
  color: white;
}

.agent-send.active:hover {
  opacity: 0.85;
  transform: scale(1.05);
}

.agent-stop {
  background: var(--color-error, #f43f5e) !important;
  color: white !important;
}

.agent-stop:hover {
  opacity: 0.85;
}

/* Markdown body styles */
.markdown-body :deep(p) {
  margin: 0 0 0.5em;
}

.markdown-body :deep(p:last-child) {
  margin-bottom: 0;
}

.markdown-body :deep(code) {
  font-family: var(--font-mono, 'JetBrains Mono', 'Fira Code', monospace);
  font-size: 0.85em;
  background: rgba(255, 255, 255, 0.06);
  padding: 0.15em 0.4em;
  border-radius: var(--radius-sm, 4px);
  color: var(--accent-blue, #00d4ff);
}

.markdown-body :deep(pre) {
  margin: 0.5em 0;
  padding: var(--space-2, 8px) var(--space-3, 12px);
  background: rgba(0, 0, 0, 0.3);
  border-radius: var(--radius-md, 6px);
  overflow-x: auto;
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.08));
}

.markdown-body :deep(pre code) {
  background: none;
  padding: 0;
  color: var(--text-primary, #e0e0e0);
  font-size: 0.8rem;
  line-height: 1.5;
}

.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3),
.markdown-body :deep(h4) {
  color: var(--text-primary, #e0e0e0);
  margin: 0.6em 0 0.3em;
  font-weight: 600;
  line-height: 1.3;
}

.markdown-body :deep(h1) { font-size: 1.1em; }
.markdown-body :deep(h2) { font-size: 1em; }
.markdown-body :deep(h3) { font-size: 0.95em; }
.markdown-body :deep(h4) { font-size: 0.9em; }

.markdown-body :deep(ul),
.markdown-body :deep(ol) {
  margin: 0.4em 0;
  padding-left: 1.5em;
}

.markdown-body :deep(li) {
  margin: 0.2em 0;
}

.markdown-body :deep(blockquote) {
  margin: 0.5em 0;
  padding: 0.3em 0.8em;
  border-left: 3px solid var(--accent-purple, #7c3aed);
  background: rgba(124, 58, 237, 0.06);
  color: var(--text-secondary);
}

.markdown-body :deep(a) {
  color: var(--accent-blue, #00d4ff);
  text-decoration: none;
}

.markdown-body :deep(a:hover) {
  text-decoration: underline;
}

.markdown-body :deep(hr) {
  border: none;
  border-top: 1px solid var(--border-default, rgba(255, 255, 255, 0.08));
  margin: 0.6em 0;
}

.markdown-body :deep(table) {
  width: 100%;
  border-collapse: collapse;
  margin: 0.5em 0;
  font-size: 0.85em;
}

.markdown-body :deep(th),
.markdown-body :deep(td) {
  padding: 0.3em 0.6em;
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.08));
  text-align: left;
}

.markdown-body :deep(th) {
  background: rgba(255, 255, 255, 0.04);
  font-weight: 600;
  color: var(--text-primary);
}

.markdown-body :deep(strong) {
  color: var(--text-primary, #e0e0e0);
  font-weight: 600;
}

.markdown-body :deep(em) {
  font-style: italic;
}

/* Tool usage display */
.tool-usages {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 8px;
}

.tool-usage {
  background: rgba(0, 0, 0, 0.25);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  padding: 6px 10px;
  font-size: 0.72rem;
}

.tool-header {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--accent-purple);
}

.tool-name {
  font-weight: 600;
  font-family: var(--font-mono);
}

.tool-done {
  font-size: 0.65rem;
  color: var(--accent-green);
  margin-left: auto;
}

.tool-spinner {
  width: 10px;
  height: 10px;
  border: 1.5px solid var(--border-default);
  border-top-color: var(--accent-purple);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  margin-left: auto;
}

.tool-args {
  margin-top: 3px;
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.68rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Thinking indicator */
.thinking-indicator {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 4px 0;
}

.thinking-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--accent-purple);
  opacity: 0.4;
  animation: thinking-pulse 1.4s ease-in-out infinite;
}

.thinking-dot:nth-child(2) {
  animation-delay: 0.2s;
}

.thinking-dot:nth-child(3) {
  animation-delay: 0.4s;
}

@keyframes thinking-pulse {
  0%, 80%, 100% {
    opacity: 0.25;
    transform: scale(0.8);
  }
  40% {
    opacity: 1;
    transform: scale(1);
    background: var(--accent-blue);
  }
}
</style>
