<script setup lang="ts">
import { ref, computed, nextTick, onMounted } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { aiApi, type AIModel, type ChatMessage, type StreamEvent, type PlanStep } from '@/api/ai'

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

function renderMarkdown(content: string): string {
  const withEmoji = content.replace(/:[a-z_]+:/g, (m) => emojiMap[m] || m)
  const raw = marked.parse(withEmoji) as string
  return DOMPurify.sanitize(raw)
}

const props = defineProps<{
  workspaceId: number
}>()

interface TextSegment {
  type: 'text'
  content: string
}

interface ToolSegment {
  type: 'tool'
  toolCallId: string
  name: string
  args: string
  result?: string
}

interface ApprovalSegment {
  type: 'approval'
  id: string
  command: string
  status: 'pending' | 'approved' | 'denied'
}

interface PlanSegment {
  type: 'plan'
  steps: PlanStep[]
}

type MessageSegment = TextSegment | ToolSegment | ApprovalSegment | PlanSegment

interface DisplayMessage {
  role: 'user' | 'assistant'
  content: string
  segments: MessageSegment[]
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
const planExpanded = ref(false)

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

function formatToolArgs(name: string, args: string): string {
  try {
    const parsed = JSON.parse(args)

    // Friendly summary for update_plan
    if (name === 'update_plan' && Array.isArray(parsed.steps)) {
      const steps = parsed.steps as { title: string; status: string }[]
      return `${steps.length} steps`
    }

    return Object.entries(parsed)
      .map(([k, v]) => {
        if (typeof v === 'string') return `${k}: ${v.length > 80 ? v.slice(0, 80) + '…' : v}`
        if (Array.isArray(v)) return `${k}: [${v.length} items]`
        if (v && typeof v === 'object') return `${k}: {…}`
        return `${k}: ${v}`
      })
      .join(', ')
  } catch {
    return args
  }
}

function isToolError(seg: ToolSegment): boolean {
  if (!seg.result) return false
  return seg.result.startsWith('Error:') || seg.result.startsWith('Command failed') || seg.result.startsWith('Command timed out')
}

async function handleApproval(seg: ApprovalSegment, approved: boolean) {
  seg.status = approved ? 'approved' : 'denied'
  try {
    await aiApi.approveCommand(seg.id, approved)
  } catch {
    // The stream will handle any errors
  }
}

async function sendMessage() {
  const text = inputValue.value.trim()
  if (!text || streaming.value) return

  if (!selectedModel.value) {
    messages.value.push({
      role: 'assistant',
      content: 'No AI model is configured. Ask an admin to set up an AI provider in Settings.',
      segments: [{ type: 'text', content: 'No AI model is configured. Ask an admin to set up an AI provider in Settings.' }],
    })
    await nextTick()
    scrollToBottom()
    return
  }

  messages.value.push({ role: 'user', content: text, segments: [] })
  inputValue.value = ''

  // Add empty assistant message for streaming
  messages.value.push({ role: 'assistant', content: '', segments: [] })
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
          const segs = messages.value[assistantIdx].segments
          const last = segs[segs.length - 1]
          if (last && last.type === 'text') {
            last.content += `\n\nError: ${event.error}`
          } else {
            segs.push({ type: 'text', content: `Error: ${event.error}` })
          }
        } else if (event.content) {
          messages.value[assistantIdx].content += event.content
          const segs = messages.value[assistantIdx].segments
          const last = segs[segs.length - 1]
          if (last && last.type === 'text') {
            last.content += event.content
          } else {
            segs.push({ type: 'text', content: event.content })
          }
        } else if (event.toolCalls) {
          for (const tc of event.toolCalls) {
            messages.value[assistantIdx].segments.push({
              type: 'tool',
              toolCallId: tc.id,
              name: tc.function.name,
              args: tc.function.arguments,
            })
          }
        } else if (event.toolResult) {
          const segs = messages.value[assistantIdx].segments
          const toolSeg = segs.find(
            (s): s is ToolSegment => s.type === 'tool' && s.toolCallId === event.toolResult!.toolCallId && !s.result
          )
          if (toolSeg) {
            toolSeg.result = event.toolResult.content
          }
        } else if (event.approvalRequired) {
          messages.value[assistantIdx].segments.push({
            type: 'approval',
            id: event.approvalRequired.id,
            command: event.approvalRequired.command,
            status: 'pending',
          })
        } else if (event.plan) {
          const segs = messages.value[assistantIdx].segments
          const existing = segs.find((s): s is PlanSegment => s.type === 'plan')
          if (existing) {
            existing.steps = event.plan
          } else {
            segs.push({ type: 'plan', steps: event.plan })
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
  planExpanded.value = false
  if (inputEl.value) {
    inputEl.value.style.height = 'auto'
  }
}

const isThinking = computed(() => {
  if (!streaming.value) return false
  const last = messages.value[messages.value.length - 1]
  if (!last || last.role !== 'assistant') return false
  return last.segments.length === 0
})

// activityStatus describes what the agent is currently doing during streaming.
const activityStatus = computed<string | null>(() => {
  if (!streaming.value) return null
  const last = messages.value[messages.value.length - 1]
  if (!last || last.role !== 'assistant') return null
  if (last.segments.length === 0) return null // isThinking handles this case

  const segs = last.segments
  const lastSeg = segs[segs.length - 1]

  // A tool is running (no result yet)
  if (lastSeg.type === 'tool' && !(lastSeg as ToolSegment).result) {
    return `Running ${lastSeg.name}…`
  }

  // Waiting for user approval
  if (lastSeg.type === 'approval' && (lastSeg as ApprovalSegment).status === 'pending') {
    return 'Waiting for approval…'
  }

  // Last segment is a completed tool or has a result — the LLM is generating the next response
  if (lastSeg.type === 'tool' && (lastSeg as ToolSegment).result) {
    return 'Thinking…'
  }

  // Last segment is text but we're still streaming — LLM is still writing
  // No status needed since the user can see text arriving
  return null
})

// Find the latest plan across all messages.
const activePlan = computed<PlanStep[] | null>(() => {
  for (let i = messages.value.length - 1; i >= 0; i--) {
    const msg = messages.value[i]
    if (msg.role !== 'assistant') continue
    for (let j = msg.segments.length - 1; j >= 0; j--) {
      const seg = msg.segments[j]
      if (seg.type === 'plan') return (seg as PlanSegment).steps
    }
  }
  return null
})

const planSummary = computed(() => {
  const steps = activePlan.value
  if (!steps || steps.length === 0) return null
  const total = steps.length
  // Find the current step: first in_progress, or first pending, or last completed
  const inProgress = steps.find((s) => s.status === 'in_progress')
  if (inProgress) {
    const idx = steps.indexOf(inProgress) + 1
    return { idx, total, title: inProgress.title, done: false }
  }
  const pending = steps.find((s) => s.status === 'pending')
  if (pending) {
    const idx = steps.indexOf(pending) + 1
    return { idx, total, title: pending.title, done: false }
  }
  // All completed or failed
  const completed = steps.filter((s) => s.status === 'completed').length
  const last = steps[steps.length - 1]
  return { idx: total, total, title: last.title, done: completed === total }
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
          <template v-for="(seg, si) in msg.segments" :key="si">
            <div v-if="seg.type === 'tool'" class="tool-usage">
              <div class="tool-header">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
                </svg>
                <span class="tool-name">{{ seg.name }}</span>
                <span v-if="!seg.result && streaming && i === messages.length - 1" class="tool-spinner" />
                <span v-else-if="seg.result && isToolError(seg as ToolSegment)" class="tool-error">&#x2718;</span>
                <span v-else-if="seg.result" class="tool-done">&#x2714;</span>
              </div>
              <div class="tool-args">{{ formatToolArgs(seg.name, seg.args) }}</div>
            </div>
            <div v-else-if="seg.type === 'approval'" class="approval-prompt">
              <div class="approval-header">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10" />
                </svg>
                <span class="approval-title">sudo command requires approval</span>
              </div>
              <code class="approval-command">{{ (seg as ApprovalSegment).command }}</code>
              <div v-if="(seg as ApprovalSegment).status === 'pending'" class="approval-actions">
                <button class="approval-btn approve" @click="handleApproval(seg as ApprovalSegment, true)">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                  Approve
                </button>
                <button class="approval-btn deny" @click="handleApproval(seg as ApprovalSegment, false)">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <line x1="18" y1="6" x2="6" y2="18" />
                    <line x1="6" y1="6" x2="18" y2="18" />
                  </svg>
                  Deny
                </button>
              </div>
              <div v-else class="approval-resolved">
                <span v-if="(seg as ApprovalSegment).status === 'approved'" class="approval-badge approved">
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                  Approved
                </span>
                <span v-else class="approval-badge denied">
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                    <line x1="18" y1="6" x2="6" y2="18" />
                    <line x1="6" y1="6" x2="18" y2="18" />
                  </svg>
                  Denied
                </span>
              </div>
            </div>
            <template v-else-if="seg.type === 'plan'" />
            <div v-else-if="seg.type === 'text' && seg.content" class="msg-text" v-html="renderMarkdown(seg.content)" />
          </template>
          <div v-if="i === messages.length - 1 && isThinking" class="thinking-indicator">
            <span class="thinking-dot" />
            <span class="thinking-dot" />
            <span class="thinking-dot" />
          </div>
          <div v-else-if="i === messages.length - 1 && activityStatus" class="activity-status">
            <span class="activity-spinner" />
            <span class="activity-label">{{ activityStatus }}</span>
          </div>
        </div>
        <div v-else class="msg-content">{{ msg.content }}</div>
      </div>
    </div>

    <!-- Sticky plan bar -->
    <div v-if="activePlan" class="plan-bar">
      <button class="plan-bar-toggle" @click="planExpanded = !planExpanded">
        <div class="plan-bar-summary">
          <span class="plan-bar-icon">
            <svg v-if="planSummary?.done" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="20 6 9 17 4 12" />
            </svg>
            <span v-else class="plan-bar-spinner" />
          </span>
          <span class="plan-bar-progress">Step {{ planSummary?.idx }} of {{ planSummary?.total }}</span>
          <span class="plan-bar-sep">&middot;</span>
          <span class="plan-bar-title">{{ planSummary?.title }}</span>
        </div>
        <svg class="plan-bar-chevron" :class="{ expanded: planExpanded }" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <polyline points="6 9 12 15 18 9" />
        </svg>
      </button>
      <div v-if="planExpanded" class="plan-bar-steps">
        <div
          v-for="(step, stepIdx) in activePlan"
          :key="stepIdx"
          class="plan-bar-step"
          :class="`plan-bar-step--${step.status}`"
        >
          <span class="plan-bar-step-icon">
            <svg v-if="step.status === 'completed'" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="20 6 9 17 4 12" />
            </svg>
            <svg v-else-if="step.status === 'failed'" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
            <span v-else-if="step.status === 'in_progress'" class="plan-dot-spinner" />
            <span v-else class="plan-dot-pending" />
          </span>
          <span class="plan-bar-step-label">{{ step.title }}</span>
        </div>
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
  padding: 0 var(--space-3);
  border-bottom: 1px solid var(--border-default);
  height: 38px;
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
  width: 22px;
  height: 22px;
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
  width: 22px;
  height: 22px;
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

.msg-text :deep(p:first-child) {
  margin-top: 0;
}

.msg-text :deep(p:last-child) {
  margin-bottom: 0;
}

/* Tool usage display */
.tool-usage {
  background: rgba(0, 0, 0, 0.25);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  padding: 6px 10px;
  font-size: 0.72rem;
  margin: 6px 0;
}

.tool-usage:first-child {
  margin-top: 0;
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
  font-size: 0.7rem;
  color: var(--accent-green);
  margin-left: auto;
}

.tool-error {
  font-size: 0.7rem;
  color: var(--accent-rose);
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

/* Sudo approval prompt */
.approval-prompt {
  background: rgba(245, 158, 11, 0.08);
  border: 1px solid rgba(245, 158, 11, 0.3);
  border-radius: var(--radius-md);
  padding: 10px 12px;
  margin: 6px 0;
}

.approval-header {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--accent-amber);
  font-size: 0.75rem;
  font-weight: 600;
}

.approval-title {
  text-transform: uppercase;
  letter-spacing: 0.03em;
  font-size: 0.68rem;
}

.approval-command {
  display: block;
  margin-top: 8px;
  padding: 6px 10px;
  background: rgba(0, 0, 0, 0.3);
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
  font-size: 0.72rem;
  color: var(--text-primary);
  word-break: break-all;
}

.approval-actions {
  display: flex;
  gap: 8px;
  margin-top: 10px;
}

.approval-btn {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 5px 14px;
  border-radius: var(--radius-md);
  font-size: 0.72rem;
  font-weight: 600;
  cursor: pointer;
  border: none;
  transition: background 150ms ease, opacity 150ms ease;
}

.approval-btn.approve {
  background: rgba(16, 185, 129, 0.15);
  color: var(--accent-green);
}

.approval-btn.approve:hover {
  background: rgba(16, 185, 129, 0.25);
}

.approval-btn.deny {
  background: rgba(244, 63, 94, 0.15);
  color: var(--accent-rose);
}

.approval-btn.deny:hover {
  background: rgba(244, 63, 94, 0.25);
}

.approval-resolved {
  margin-top: 8px;
}

.approval-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 0.68rem;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 9999px;
}

.approval-badge.approved {
  background: rgba(16, 185, 129, 0.15);
  color: var(--accent-green);
}

.approval-badge.denied {
  background: rgba(244, 63, 94, 0.15);
  color: var(--accent-rose);
}

/* Sticky plan bar */
.plan-bar {
  flex-shrink: 0;
  border-top: 1px solid rgba(124, 58, 237, 0.2);
  background: linear-gradient(135deg, rgba(124, 58, 237, 0.06), rgba(0, 212, 255, 0.03));
}

.plan-bar-toggle {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 7px 12px;
  background: none;
  border: none;
  cursor: pointer;
  color: var(--text-secondary);
  transition: background 150ms ease;
}

.plan-bar-toggle:hover {
  background: rgba(124, 58, 237, 0.06);
}

.plan-bar-summary {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.plan-bar-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  flex-shrink: 0;
  color: var(--accent-green);
}

.plan-bar-spinner {
  width: 10px;
  height: 10px;
  border: 1.5px solid rgba(124, 58, 237, 0.25);
  border-top-color: var(--accent-purple);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.plan-bar-progress {
  font-size: 0.7rem;
  font-weight: 700;
  color: var(--accent-purple);
  white-space: nowrap;
}

.plan-bar-sep {
  color: var(--text-muted);
  font-size: 0.7rem;
}

.plan-bar-title {
  font-size: 0.7rem;
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.plan-bar-chevron {
  flex-shrink: 0;
  color: var(--text-muted);
  transition: transform 150ms ease;
  transform: rotate(180deg);
}

.plan-bar-chevron.expanded {
  transform: rotate(0deg);
}

.plan-bar-steps {
  padding: 0 12px 8px;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.plan-bar-step {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.7rem;
  padding: 3px 6px;
  border-radius: var(--radius-sm, 4px);
}

.plan-bar-step-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  flex-shrink: 0;
}

.plan-bar-step--completed .plan-bar-step-icon {
  color: var(--accent-green);
}

.plan-bar-step--completed .plan-bar-step-label {
  color: var(--text-muted);
  text-decoration: line-through;
  text-decoration-color: rgba(255, 255, 255, 0.12);
}

.plan-bar-step--failed .plan-bar-step-icon {
  color: var(--accent-rose);
}

.plan-bar-step--failed .plan-bar-step-label {
  color: var(--accent-rose);
}

.plan-bar-step--in_progress {
  background: rgba(124, 58, 237, 0.06);
}

.plan-bar-step--in_progress .plan-bar-step-label {
  color: var(--text-primary);
  font-weight: 600;
}

.plan-bar-step--pending .plan-bar-step-label {
  color: var(--text-muted);
}

.plan-dot-spinner {
  width: 9px;
  height: 9px;
  border: 1.5px solid rgba(124, 58, 237, 0.25);
  border-top-color: var(--accent-purple);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.plan-dot-pending {
  width: 7px;
  height: 7px;
  border: 1.5px solid var(--border-default);
  border-radius: 50%;
  opacity: 0.5;
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

/* Activity status indicator — shown during tool execution and between LLM calls */
.activity-status {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 0 2px;
}

.activity-spinner {
  width: 12px;
  height: 12px;
  border: 1.5px solid rgba(124, 58, 237, 0.2);
  border-top-color: var(--accent-purple);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  flex-shrink: 0;
}

.activity-label {
  font-size: 0.72rem;
  color: var(--text-muted);
  font-style: italic;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
