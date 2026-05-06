<script setup lang="ts">
import { ref, computed, nextTick, onMounted } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { aiApi, type AIModel, type ChatMessage, type StreamEvent, type PlanStep, type ToolCall } from '@/api/ai'
import { useConversationStore } from '@/stores/chatHistory'

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

interface ThinkingSegment {
  type: 'thinking'
  content: string
}

type MessageSegment = TextSegment | ToolSegment | ApprovalSegment | PlanSegment | ThinkingSegment

interface DisplayMessage {
  role: 'user' | 'assistant'
  content: string
  segments: MessageSegment[]
}

const conversationStore = useConversationStore()
const activeConversationId = ref<number | null>(null)
const historyOpen = ref(false)

const messages = ref<DisplayMessage[]>([])
// rawMessages tracks the full API message history including role:"tool" messages.
// It is the source of truth for saving; messages is the source of truth for rendering.
const rawMessages = ref<ChatMessage[]>([])

const inputValue = ref('')
const chatBody = ref<HTMLElement | null>(null)
const models = ref<AIModel[]>([])
const selectedModel = ref('')
const thinkingPreferences = ref<Record<string, boolean>>({})
const streaming = ref(false)
const abortController = ref<AbortController | null>(null)
const inputFocused = ref(false)
const inputEl = ref<HTMLTextAreaElement | null>(null)
const planExpanded = ref(false)

const currentModel = computed(() => models.value.find((model) => model.id === selectedModel.value) ?? null)

const canToggleThinking = computed(() => {
  const model = currentModel.value
  return Boolean(model?.thinking.supported && model.thinking.canDisable)
})

const thinkingEnabled = computed(() => {
  const model = currentModel.value
  if (!model?.thinking.supported) {
    return false
  }

  const override = thinkingPreferences.value[model.id]
  if (override !== undefined) {
    return override
  }

  return model.thinking.enabledByDefault
})

const thinkingRequest = computed(() => {
  const model = currentModel.value
  if (!model?.thinking.supported) {
    return undefined
  }

  return { enabled: thinkingEnabled.value }
})

function autoResize() {
  const el = inputEl.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 120) + 'px'
}

function resetInputHeight() {
  const el = inputEl.value
  if (!el) return
  el.style.height = 'auto'
}

function toggleThinking() {
  const model = currentModel.value
  if (!model || !canToggleThinking.value) return

  thinkingPreferences.value = {
    ...thinkingPreferences.value,
    [model.id]: !thinkingEnabled.value,
  }
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
  // Fetch conversation history for this workspace (non-blocking).
  conversationStore.fetchConversations(props.workspaceId)
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

function reconstructAssistantDisplay(rawMsgs: ChatMessage[], start: number, end: number): DisplayMessage {
  const display: DisplayMessage = {
    role: 'assistant',
    content: '',
    segments: [],
  }

  for (let i = start; i < end; i++) {
    const msg = rawMsgs[i]
    if (msg.role !== 'assistant') {
      continue
    }

    if (msg.reasoning_content) {
      display.segments.push({ type: 'thinking', content: msg.reasoning_content })
    }

    if (msg.content) {
      display.content += msg.content
      const lastSeg = display.segments[display.segments.length - 1]
      if (lastSeg && lastSeg.type === 'text') {
        lastSeg.content += msg.content
      } else {
        display.segments.push({ type: 'text', content: msg.content })
      }
    }

    if (msg.tool_calls && msg.tool_calls.length > 0) {
      const toolResults = new Map<string, string>()
      for (let j = i + 1; j < end && rawMsgs[j].role === 'tool'; j++) {
        const toolCallID = rawMsgs[j].tool_call_id
        if (toolCallID) {
          toolResults.set(toolCallID, rawMsgs[j].content)
        }
      }

      for (const tc of msg.tool_calls) {
        display.segments.push({
          type: 'tool',
          toolCallId: tc.id,
          name: tc.function.name,
          args: tc.function.arguments,
          result: toolResults.get(tc.id),
        })
      }
    }
  }

  return display
}

// reconstructDisplayMessages converts a raw ChatMessage[] into DisplayMessage[] for rendering.
// Assistant/tool rounds are merged back into a single assistant bubble until the next user message.
function reconstructDisplayMessages(rawMsgs: ChatMessage[]): DisplayMessage[] {
  const display: DisplayMessage[] = []

  for (let i = 0; i < rawMsgs.length;) {
    const msg = rawMsgs[i]
    if (msg.role === 'user') {
      display.push({ role: 'user', content: msg.content, segments: [] })
      i++
      continue
    }

    if (msg.role === 'assistant') {
      let end = i + 1
      for (; end < rawMsgs.length && rawMsgs[end].role !== 'user'; end++) {
        // Walk through assistant/tool rounds until the next user message.
      }
      display.push(reconstructAssistantDisplay(rawMsgs, i, end))
      i = end
      continue
    }

    // role:"tool" messages are rendered as segments on the surrounding assistant bubble.
    i++
  }

  return display
}

async function loadConversation(convId: number) {
  try {
    const res = await aiApi.getMessages(convId)
    rawMessages.value = res.messages
    messages.value = reconstructDisplayMessages(res.messages)
    activeConversationId.value = convId
    conversationStore.setActive(convId)
    historyOpen.value = false
    await nextTick()
    scrollToBottom()
  } catch {
    // Leave current state intact on failure.
  }
}

async function saveCurrentConversation() {
  // If no messages to save, skip.
  if (rawMessages.value.length === 0) return

  // Create a conversation on first save for this session.
  if (activeConversationId.value === null) {
    try {
      const conv = await conversationStore.createConversation(props.workspaceId, selectedModel.value)
      activeConversationId.value = conv.id
      conversationStore.setActive(conv.id)
    } catch {
      // Could not create conversation — skip save to avoid losing chat flow.
      return
    }
  }

  const convId = activeConversationId.value
  if (convId === null) return

  try {
    await aiApi.saveMessages(convId, rawMessages.value)
    // Refresh the conversation list so the title/updatedAt updates are reflected.
    conversationStore.fetchConversations(props.workspaceId)
  } catch (err) {
    console.error('Failed to save conversation messages:', err)
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

  // Push user message to display and raw arrays.
  messages.value.push({ role: 'user', content: text, segments: [] })
  rawMessages.value.push({ role: 'user', content: text })
  inputValue.value = ''
  resetInputHeight()

  // Add empty assistant message for streaming.
  messages.value.push({ role: 'assistant', content: '', segments: [] })
  const assistantIdx = messages.value.length - 1

  await nextTick()
  scrollToBottom()

  streaming.value = true
  const controller = new AbortController()
  abortController.value = controller

  // Build the API payload from rawMessages (excludes the empty assistant placeholder).
  const chatMessages: ChatMessage[] = rawMessages.value.slice()

  // Each LLM iteration is tracked as a "round" so we can reconstruct the correct
  // interleaved assistant/tool message sequence for rawMessages on completion.
  interface Round {
    content: string
    reasoningContent: string
    thinkingState: unknown
    toolCalls: ToolCall[]
    toolResults: ChatMessage[]
  }
  const rounds: Round[] = [{ content: '', reasoningContent: '', thinkingState: undefined, toolCalls: [], toolResults: [] }]
  let streamFailed = false

  try {
    await aiApi.agentStream(
      selectedModel.value,
      chatMessages,
      props.workspaceId,
      (event: StreamEvent) => {
        if (event.error) {
          streamFailed = true
          messages.value[assistantIdx].content += `\n\nError: ${event.error}`
          const segs = messages.value[assistantIdx].segments
          const last = segs[segs.length - 1]
          if (last && last.type === 'text') {
            last.content += `\n\nError: ${event.error}`
          } else {
            segs.push({ type: 'text', content: `Error: ${event.error}` })
          }
        }

        if (event.reasoningContent) {
          // Reasoning before content marks the start of a new LLM round after tool results.
          if (rounds[rounds.length - 1].toolResults.length > 0) {
            rounds.push({ content: '', reasoningContent: '', thinkingState: undefined, toolCalls: [], toolResults: [] })
          }
          const segs = messages.value[assistantIdx].segments
          const lastSeg = segs[segs.length - 1]
          if (lastSeg && lastSeg.type === 'thinking') {
            lastSeg.content += event.reasoningContent
          } else {
            segs.push({ type: 'thinking', content: event.reasoningContent })
          }
          rounds[rounds.length - 1].reasoningContent += event.reasoningContent
        }

        if (event.thinkingState !== undefined) {
          // ThinkingState before content also signals a new round when tool results are present.
          if (rounds[rounds.length - 1].toolResults.length > 0) {
            rounds.push({ content: '', reasoningContent: '', thinkingState: undefined, toolCalls: [], toolResults: [] })
          }
          rounds[rounds.length - 1].thinkingState = event.thinkingState
        }

        if (event.content) {
          // A content event after tool results signals the start of a new LLM round.
          if (rounds[rounds.length - 1].toolResults.length > 0) {
            rounds.push({ content: '', reasoningContent: '', thinkingState: undefined, toolCalls: [], toolResults: [] })
          }
          rounds[rounds.length - 1].content += event.content
          messages.value[assistantIdx].content += event.content
          const segs = messages.value[assistantIdx].segments
          const last = segs[segs.length - 1]
          if (last && last.type === 'text') {
            last.content += event.content
          } else {
            segs.push({ type: 'text', content: event.content })
          }
        }

        if (event.toolCalls) {
          // The server sends all tool calls for one LLM iteration in a single event
          // before executing any of them. If there are prior tool results in the current
          // round, this batch belongs to the next iteration — start a fresh round.
          if (rounds[rounds.length - 1].toolResults.length > 0) {
            rounds.push({ content: '', reasoningContent: '', thinkingState: undefined, toolCalls: [], toolResults: [] })
          }
          for (const tc of event.toolCalls) {
            rounds[rounds.length - 1].toolCalls.push({ id: tc.id, itemId: tc.itemId, type: tc.type, function: { name: tc.name, arguments: tc.arguments } })
            messages.value[assistantIdx].segments.push({
              type: 'tool',
              toolCallId: tc.id,
              name: tc.name,
              args: tc.arguments,
            })
          }
        }

        if (event.toolResult) {
          const segs = messages.value[assistantIdx].segments
          const toolSeg = segs.find(
            (s): s is ToolSegment => s.type === 'tool' && s.toolCallId === event.toolResult!.toolCallId && !s.result
          )
          if (toolSeg) {
            toolSeg.result = event.toolResult.content
          }
          rounds[rounds.length - 1].toolResults.push({
            role: 'tool',
            content: event.toolResult.content,
            tool_call_id: event.toolResult.toolCallId,
          })
        }

        if (event.approvalRequired) {
          messages.value[assistantIdx].segments.push({
            type: 'approval',
            id: event.approvalRequired.id,
            command: event.approvalRequired.command,
            status: 'pending',
          })
        }

        if (event.plan) {
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
      thinkingRequest.value,
    )
  } catch (err: any) {
    streamFailed = true
    if (err.name !== 'AbortError') {
      messages.value[assistantIdx].content =
        messages.value[assistantIdx].content || `Error: ${err.message}`
    }
  } finally {
    if (!streamFailed) {
      // Flush each LLM round to rawMessages: assistant message followed by its tool results.
      // This preserves the exact interleaved structure the provider saw across iterations.
      for (const round of rounds) {
        const assistantRaw: ChatMessage = { role: 'assistant', content: round.content }
        if (round.reasoningContent) assistantRaw.reasoning_content = round.reasoningContent
        if (round.thinkingState !== undefined) assistantRaw.thinking_state = round.thinkingState
        if (round.toolCalls.length > 0) assistantRaw.tool_calls = round.toolCalls
        rawMessages.value.push(assistantRaw)
        for (const toolMsg of round.toolResults) rawMessages.value.push(toolMsg)
      }
    } else {
      // Revert the user message pushed at the start — don't persist incomplete turns.
      if (rawMessages.value.length > 0 && rawMessages.value[rawMessages.value.length - 1].role === 'user') {
        rawMessages.value.pop()
      }
    }

    streaming.value = false
    abortController.value = null
    await nextTick()
    scrollToBottom()

    // Auto-save only on clean completion — skip on abort or error.
    if (!streamFailed) {
      saveCurrentConversation().catch((err) => {
        console.error('Auto-save failed:', err)
      })
    }
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
  rawMessages.value = []
  inputValue.value = ''
  planExpanded.value = false
  resetInputHeight()
  activeConversationId.value = null
  conversationStore.setActive(null)
}

// Conversations filtered to the current workspace.
const workspaceConversations = computed(() =>
  conversationStore.conversations.filter((c) => c.workspaceId === props.workspaceId),
)

function formatRelativeTime(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMin = Math.floor(diffMs / 60000)
  if (diffMin < 1) return 'just now'
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h ago`
  const diffDay = Math.floor(diffHr / 24)
  if (diffDay < 7) return `${diffDay}d ago`
  return date.toLocaleDateString()
}

async function deleteConversation(id: number) {
  if (activeConversationId.value === id) {
    newChat()
  }
  await conversationStore.deleteConversation(id)
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
            <path d="m12 3-1.912 5.813a2 2 0 0 1-1.275 1.275L3 12l5.813 1.912a2 2 0 0 1 1.275 1.275L12 21l1.912-5.813a2 2 0 0 1 1.275-1.275L21 12l-5.813-1.912a2 2 0 0 1-1.275-1.275L12 3Z" />
            <path d="M5 3v4" />
            <path d="M19 17v4" />
            <path d="M3 5h4" />
            <path d="M17 19h4" />
          </svg>
        </span>
        <span class="agent-title">AI Agent</span>
      </div>
      <div class="agent-header-actions">
        <button
          class="history-btn"
          :class="{ active: historyOpen }"
          title="Chat history"
          @click="historyOpen = !historyOpen"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" />
            <path d="M3 3v5h5" />
            <path d="M12 7v5l4 2" />
          </svg>
        </button>
        <button class="new-chat-btn" title="New Chat" @click="newChat">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 5v14" />
            <path d="M5 12h14" />
          </svg>
        </button>
      </div>
    </div>

    <!-- History panel -->
    <div v-if="historyOpen" class="history-panel">
      <div class="history-header">
        <span class="history-title">History</span>
      </div>
      <div class="history-list">
        <div v-if="workspaceConversations.length === 0" class="history-empty">
          No past conversations
        </div>
        <div
          v-for="conv in workspaceConversations"
          :key="conv.id"
          class="history-item"
          :class="{ active: conv.id === activeConversationId }"
          @click="loadConversation(conv.id)"
        >
          <div class="history-item-main">
            <span class="history-item-title">{{ conv.title || 'Untitled' }}</span>
            <span class="history-item-meta">
              <span class="history-item-model">{{ conv.model }}</span>
              <span class="history-item-sep">&middot;</span>
              <span class="history-item-time">{{ formatRelativeTime(conv.updatedAt) }}</span>
            </span>
          </div>
          <button
            class="history-item-delete"
            title="Delete conversation"
            @click.stop="deleteConversation(conv.id)"
          >
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
      </div>
    </div>

    <div ref="chatBody" class="agent-body">
      <div v-if="messages.length === 0" class="chat-empty">
        <div class="empty-icon">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="m12 3-1.912 5.813a2 2 0 0 1-1.275 1.275L3 12l5.813 1.912a2 2 0 0 1 1.275 1.275L12 21l1.912-5.813a2 2 0 0 1 1.275-1.275L21 12l-5.813-1.912a2 2 0 0 1-1.275-1.275L12 3Z" />
            <path d="M5 3v4" />
            <path d="M19 17v4" />
            <path d="M3 5h4" />
            <path d="M17 19h4" />
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
              <path d="m12 3-1.912 5.813a2 2 0 0 1-1.275 1.275L3 12l5.813 1.912a2 2 0 0 1 1.275 1.275L12 21l1.912-5.813a2 2 0 0 1 1.275-1.275L21 12l-5.813-1.912a2 2 0 0 1-1.275-1.275L12 3Z" />
              <path d="M5 3v4" />
              <path d="M19 17v4" />
              <path d="M3 5h4" />
              <path d="M17 19h4" />
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
            <details v-if="seg.type === 'thinking'" class="thinking-content">
              <summary class="thinking-summary">Thinking</summary>
              <p class="thinking-text">{{ (seg as ThinkingSegment).content }}</p>
            </details>
            <div v-else-if="seg.type === 'tool'" class="tool-usage">
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
      <div class="input-shell" :class="{ focused: inputFocused }">
        <div class="input-container" @click="inputEl?.focus()">
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
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25" stroke-linecap="round" stroke-linejoin="round">
              <path d="m22 2-7 20-4-9-9-4Z" />
              <path d="M22 2 11 13" />
            </svg>
          </button>
        </div>
        <div class="input-toolbar">
          <template v-if="models.length > 0">
            <select v-model="selectedModel" class="model-selector">
              <option v-for="m in models" :key="m.id" :value="m.id">{{ m.name }}</option>
            </select>
            <button
              v-if="canToggleThinking"
              type="button"
              class="thinking-toggle"
              :class="{ active: thinkingEnabled }"
              :aria-pressed="thinkingEnabled"
              :title="thinkingEnabled ? 'Thinking is enabled for this model.' : 'Thinking is disabled for this model.'"
              @click="toggleThinking"
            >
              <span class="thinking-toggle-label">Thinking</span>
              <span class="thinking-toggle-state">{{ thinkingEnabled ? 'On' : 'Off' }}</span>
            </button>
          </template>
          <span v-else class="agent-badge">No Models</span>
        </div>
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
  border-bottom: 0.5px solid var(--border-default);
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
  width: 16px;
  height: 16px;
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: var(--bg-base);
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
  font-size: 10.5px;
  font-weight: 500;
  font-family: var(--font-mono);
  padding: 1px 7px;
  border-radius: var(--radius-md);
  background: var(--bg-raised);
  color: var(--text-tertiary);
  border: 0.5px solid var(--border-subtle);
}

.new-chat-btn,
.history-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--text-secondary);
  border: 0.5px solid var(--border-default);
  cursor: pointer;
  transition: all var(--transition-fast);
}

.new-chat-btn:hover,
.history-btn:hover {
  background: var(--bg-raised);
  color: var(--text-primary);
  border-color: var(--border-strong);
}

.history-btn.active {
  background: var(--accent-glow);
  color: var(--accent);
  border-color: var(--accent-border);
}

.model-selector {
  font-size: 0.75rem;
  padding: 2px 8px;
  border-radius: var(--radius-md);
  background: var(--bg-surface-alt);
  border: 0.5px solid var(--border-default);
  color: var(--text-secondary);
  outline: none;
  cursor: pointer;
  transition: border-color var(--transition-fast);
}

.model-selector:focus {
  border-color: var(--accent-border);
}

.thinking-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 8px;
  border-radius: var(--radius-md);
  background: var(--bg-surface-alt);
  border: 0.5px solid var(--border-default);
  color: var(--text-secondary);
  font-size: 0.72rem;
  transition: border-color var(--transition-fast), background var(--transition-fast), color var(--transition-fast);
}

.thinking-toggle:hover {
  border-color: var(--border-strong);
  color: var(--text-primary);
}

.thinking-toggle.active {
  background: var(--accent-glow);
  border-color: var(--accent-border);
  color: var(--accent);
}

.thinking-toggle-label {
  font-weight: 500;
}

.thinking-toggle-state {
  font-family: var(--font-mono);
  font-size: 0.68rem;
  color: var(--text-tertiary);
}

.thinking-toggle.active .thinking-toggle-state {
  color: var(--accent);
}

/* History panel */
.history-panel {
  flex-shrink: 0;
  border-bottom: 0.5px solid var(--border-default);
  background: var(--bg-surface-alt);
  max-height: 220px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.history-header {
  padding: 6px 12px;
  border-bottom: 0.5px solid var(--border-subtle);
  flex-shrink: 0;
}

.history-title {
  font-size: 0.72rem;
  font-weight: 600;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.history-list {
  flex: 1;
  overflow-y: auto;
  padding: 4px 0;
}

.history-empty {
  padding: 12px;
  font-size: 0.75rem;
  color: var(--text-muted);
  text-align: center;
}

.history-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  cursor: pointer;
  transition: background 100ms ease;
  border-radius: var(--radius-sm);
  margin: 0 4px;
}

.history-item:hover {
  background: var(--bg-hover);
}

.history-item.active {
  background: var(--accent-glow);
}

.history-item-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.history-item-title {
  font-size: 0.75rem;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.history-item.active .history-item-title {
  color: var(--accent);
}

.history-item-meta {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 0.68rem;
  color: var(--text-muted);
}

.history-item-model {
  font-family: var(--font-mono);
  font-size: 0.65rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 80px;
}

.history-item-sep {
  opacity: 0.5;
}

.history-item-time {
  white-space: nowrap;
}

.history-item-delete {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: var(--radius-sm);
  background: transparent;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  opacity: 0;
  transition: opacity 100ms ease, background 100ms ease, color 100ms ease;
}

.history-item:hover .history-item-delete {
  opacity: 1;
}

.history-item-delete:hover {
  background: var(--error-bg);
  color: var(--accent-rose);
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
  background: var(--bg-raised);
  color: var(--text-tertiary);
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
  background: var(--accent);
  color: var(--bg-base);
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
  background: var(--bg-raised);
  color: var(--text-primary);
}

.agent-input-area {
  padding: var(--space-3);
  border-top: 0.5px solid var(--border-default);
  flex-shrink: 0;
}

.input-shell {
  display: flex;
  flex-direction: column;
  background: var(--bg-surface-alt);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg, 12px);
  transition: border-color var(--transition-fast), box-shadow var(--transition-fast);
}

.input-shell.focused {
  border-color: var(--accent-border);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.input-container {
  display: flex;
  align-items: flex-end;
  gap: var(--space-2);
  padding: var(--space-2, 8px);
  padding-left: var(--space-3, 12px);
  cursor: text;
}

.input-toolbar {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
  padding: 0 var(--space-2, 8px) var(--space-2, 8px);
  padding-left: var(--space-3, 12px);
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

.agent-input:focus-visible {
  box-shadow: none;
}

.agent-input::placeholder {
  color: var(--text-muted);
}

.agent-send {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: var(--radius-md);
  background: var(--bg-hover);
  color: var(--text-tertiary);
  flex-shrink: 0;
  transition: all var(--transition-fast);
  cursor: pointer;
  border: none;
}

.agent-send.active {
  background: var(--accent);
  color: var(--bg-base);
}

.agent-send.active:hover {
  opacity: 0.85;
  transform: scale(1.05);
}

.agent-stop {
  background: var(--warning) !important;
  color: var(--bg-base) !important;
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
  font-family: var(--font-mono);
  font-size: 0.85em;
  background: var(--bg-raised);
  padding: 0.15em 0.4em;
  border-radius: var(--radius-sm);
  color: var(--accent);
}

.markdown-body :deep(pre) {
  margin: 0.5em 0;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-void);
  border-radius: var(--radius-md);
  overflow-x: auto;
  border: 0.5px solid var(--border-subtle);
}

.markdown-body :deep(pre code) {
  background: none;
  padding: 0;
  color: var(--text-primary);
  font-size: 0.8rem;
  line-height: 1.5;
}

.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3),
.markdown-body :deep(h4) {
  color: var(--text-primary);
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
  padding: 4px 8px 4px 12px;
  border-left: 1.5px solid var(--accent);
  background: var(--accent-glow);
  color: var(--text-secondary);
  border-radius: 0 var(--radius-md) var(--radius-md) 0;
}

.markdown-body :deep(a) {
  color: var(--accent);
  text-decoration: none;
}

.markdown-body :deep(a:hover) {
  text-decoration: underline;
}

.markdown-body :deep(hr) {
  border: none;
  border-top: 0.5px solid var(--border-subtle);
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
  border: 0.5px solid var(--border-default);
  text-align: left;
}

.markdown-body :deep(th) {
  background: var(--bg-hover);
  font-weight: 600;
  color: var(--text-primary);
}

.markdown-body :deep(strong) {
  color: var(--text-primary);
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
  background: var(--bg-void);
  border: 0.5px solid var(--border-default);
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
  color: var(--text-secondary);
}

.tool-name {
  font-weight: 600;
  font-family: var(--font-mono);
}

.tool-done {
  font-size: 0.75rem;
  color: var(--accent-green);
  margin-left: auto;
}

.tool-error {
  font-size: 0.75rem;
  color: var(--accent-rose);
  margin-left: auto;
}

.tool-spinner {
  width: 10px;
  height: 10px;
  border: 1.5px solid var(--border-subtle);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  margin-left: auto;
}

.tool-args {
  margin-top: 3px;
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.72rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Sudo approval prompt */
.approval-prompt {
  background: var(--warning-bg);
  border: 0.5px solid var(--warning-border);
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
  font-size: 0.72rem;
}

.approval-command {
  display: block;
  margin-top: 8px;
  padding: 6px 10px;
  background: var(--bg-void);
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
  background: var(--success-bg);
  color: var(--accent-green);
}

.approval-btn.approve:hover {
  background: var(--success-bg);
}

.approval-btn.deny {
  background: var(--error-bg);
  color: var(--accent-rose);
}

.approval-btn.deny:hover {
  background: var(--error-bg);
}

.approval-resolved {
  margin-top: 8px;
}

.approval-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 0.72rem;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 9999px;
}

.approval-badge.approved {
  background: var(--success-bg);
  color: var(--accent-green);
}

.approval-badge.denied {
  background: var(--error-bg);
  color: var(--accent-rose);
}

/* Sticky plan bar */
.plan-bar {
  flex-shrink: 0;
  border-top: 0.5px solid var(--border-subtle);
  background: var(--bg-elevated);
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
  background: var(--bg-hover);
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
  border: 1.5px solid var(--border-subtle);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.plan-bar-progress {
  font-size: 11px;
  font-weight: 500;
  color: var(--accent);
  white-space: nowrap;
}

.plan-bar-sep {
  color: var(--text-muted);
  font-size: 0.75rem;
}

.plan-bar-title {
  font-size: 0.75rem;
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
  font-size: 0.75rem;
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
  text-decoration-color: var(--text-decoration-muted);
}

.plan-bar-step--failed .plan-bar-step-icon {
  color: var(--accent-rose);
}

.plan-bar-step--failed .plan-bar-step-label {
  color: var(--accent-rose);
}

.plan-bar-step--in_progress {
  background: var(--accent-glow);
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
  border: 1.5px solid var(--border-subtle);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.plan-dot-pending {
  width: 7px;
  height: 7px;
  border: 0.5px solid var(--border-subtle);
  border-radius: 50%;
  opacity: 0.5;
}

/* Thinking content (reasoning output) */
.thinking-content {
  margin-bottom: 8px;
}

.thinking-summary {
  font-size: 11px;
  color: var(--text-muted);
  cursor: pointer;
  user-select: none;
  list-style: none;
  display: flex;
  align-items: center;
  gap: 4px;
  opacity: 0.7;
}

.thinking-summary::-webkit-details-marker {
  display: none;
}

.thinking-summary::before {
  content: '▶';
  font-size: 8px;
  transition: transform 0.15s ease;
}

details[open] .thinking-summary::before {
  transform: rotate(90deg);
}

.thinking-text {
  margin: 4px 0 0;
  padding: 6px 10px;
  font-style: italic;
  font-size: 12px;
  color: var(--text-muted);
  border-left: 2px solid var(--border-subtle);
  white-space: pre-wrap;
  line-height: 1.5;
  opacity: 0.8;
}

/* Thinking indicator */
.thinking-indicator {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 4px 0;
}

.thinking-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--accent);
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
    background: var(--accent);
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
  border: 1.5px solid var(--border-subtle);
  border-top-color: var(--accent);
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
