<script setup lang="ts">
import { ref, computed, nextTick, onMounted, onUnmounted, watch } from 'vue'
import { aiApi, type AIModel, type ChatMessage, type StreamEvent, type PlanStep, type ToolCall, type ToolResult, type ApprovalResult, type ContextSize, type UserQuestion, type UserQuestionAnswer, type UserQuestionOption, type UserQuestionRequest, type UserQuestionResult } from '@/api/ai'
import { useConversationStore } from '@/stores/chatHistory'
import { useAgentRunStore, isAgentRunActiveStatus, type AgentRun } from '@/stores/agentRuns'
import { useAiAgentStore } from '@/stores/aiAgents'
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import AiToolGroup from '@/components/ide/AiToolGroup.vue'
import AiThinkingSection from '@/components/ide/AiThinkingSection.vue'
import AiMessageCopyButton from '@/components/ide/AiMessageCopyButton.vue'
import AiQuestionCard from '@/components/ide/AiQuestionCard.vue'
import {
  hasToolResult,
  planStepsFromToolArgs,
  shouldRenderToolCall,
  toolGroupKey,
  type ToolDisplaySegment,
  type ToolGroupDisplay,
} from '@/components/ide/aiToolDisplay'

const props = defineProps<{
  workspaceId: number
  selectedAgentId: string
  focusedRunId?: number | null
}>()

const emit = defineEmits<{
  (e: 'clear-focused-run'): void
  (e: 'select-agent', agentId: string): void
}>()

interface TextSegment {
  type: 'text'
  content: string
}

interface ToolSegment extends ToolDisplaySegment {
  type: 'tool'
}

interface ApprovalSegment {
  type: 'approval'
  id: string
  command: string
  status: 'pending' | ApprovalResult['status']
  error?: string
}

interface QuestionSegment {
  type: 'question'
  request: UserQuestionRequest
  status: 'pending' | UserQuestionResult['status']
  answers: UserQuestionAnswer[]
  selections: Record<string, string[]>
  customAnswers: Record<string, string>
  error?: string
}

interface PlanSegment {
  type: 'plan'
  steps: PlanStep[]
}

interface ThinkingSegment {
  type: 'thinking'
  content: string
}

type MessageSegment = TextSegment | ToolSegment | ApprovalSegment | QuestionSegment | PlanSegment | ThinkingSegment

type MessageRenderItem = MessageSegment | ToolGroupDisplay

interface DisplayMessage {
  role: 'user' | 'assistant'
  content: string
  segments: MessageSegment[]
}

interface SlashCommand {
  name: string
  description: string
  prompt: string
}

const conversationStore = useConversationStore()
const agentRunStore = useAgentRunStore()
const aiAgentStore = useAiAgentStore()
const DEFAULT_AI_MODEL_ID = 'mistral-medium-3-5'
const slashCommands: SlashCommand[] = [
  {
    name: '/init',
    description: 'Create or update AGENTS.md with project instructions',
    prompt: `Initialize this repository for future AI coding agents by creating or updating AGENTS.md at the workspace root.

Inspect the repository structure, README, package or build configuration, and any existing AGENTS.md first. Then write a concise AGENTS.md that captures:
- Project overview and architecture
- Common development, build, test, and lint commands that actually exist in the repository
- Code conventions and workflow constraints useful to future agents
- Any important notes about generated files or files that should not be edited manually

If AGENTS.md already exists, preserve useful guidance and update stale or missing sections instead of replacing it blindly. Keep the file specific to this repository, avoid inventing commands, and briefly summarize what changed after writing it.`,
  },
]
const activeConversationId = ref<number | null>(null)

const messages = ref<DisplayMessage[]>([])
// rawMessages tracks the full API message history including role:"tool" messages.
// It is the source of truth for saving; messages is the source of truth for rendering.
const rawMessages = ref<ChatMessage[]>([])

const inputValue = ref('')
const chatBody = ref<HTMLElement | null>(null)
const models = ref<AIModel[]>([])
const selectedModel = ref('')
const thinkingPreferences = ref<Record<string, boolean>>({})
const thinkingEffortPreferences = ref<Record<string, string>>({})
const streaming = ref(false)
const abortController = ref<AbortController | null>(null)
const activeAgentRunId = ref<number | null>(null)
const continuationParentRunId = ref<number | null>(null)
const inputFocused = ref(false)
const inputEl = ref<HTMLTextAreaElement | null>(null)
const planExpanded = ref(false)
const focusedRunMessages = ref<DisplayMessage[]>([])
const focusedRunLoading = ref(false)
const focusedRunError = ref<string | null>(null)
const focusedRunLastSequence = ref(0)
const focusedRunEventController = ref<AbortController | null>(null)
const focusedRunOutputStarted = ref(false)
const activeContextSize = ref<ContextSize | null>(null)
const focusedRunContextSize = ref<ContextSize | null>(null)
const autoScrollEnabled = ref(true)

const currentModel = computed(() => models.value.find((model) => model.id === selectedModel.value) ?? null)

const viewingFocusedRun = computed(() => props.focusedRunId !== null && props.focusedRunId !== undefined)

const focusedRun = computed(() => {
  if (!props.focusedRunId) return null
  return agentRunStore.runs.find((run) => run.id === props.focusedRunId) ?? null
})

const focusedRunTitle = computed(() => (
  focusedRun.value?.promptPreview?.trim() || (props.focusedRunId ? `Agent Run #${props.focusedRunId}` : currentAgentName.value)
))

const currentAgentName = computed(() => (
  aiAgentStore.agents.find((agent) => agent.id === props.selectedAgentId)?.name ?? 'AI Agent'
))

const focusedRunStatus = computed(() => (
  focusedRun.value?.status.replace('_', ' ') ?? 'loading'
))

const focusedRunActive = computed(() => (
  focusedRun.value ? isAgentRunActiveStatus(focusedRun.value.status) : false
))

const canContinueFocusedRun = computed(() => Boolean(focusedRun.value?.conversationId) && !focusedRunActive.value)

const visibleMessages = computed(() => (
  viewingFocusedRun.value ? focusedRunMessages.value : messages.value
))

const displayStreaming = computed(() => (
  viewingFocusedRun.value ? focusedRunActive.value : streaming.value
))

const displayedContextSize = computed(() => (
  viewingFocusedRun.value ? focusedRunContextSize.value : activeContextSize.value
))

const canToggleThinking = computed(() => {
  const model = currentModel.value
  return Boolean(model?.thinking.supported && model.thinking.canDisable)
})

const canSelectThinkingEffort = computed(() => {
  const model = currentModel.value
  return Boolean(model?.thinking.supported && (model.thinking.supportedEfforts?.length ?? 0) > 0)
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

function defaultThinkingEffort(model: AIModel): string {
  return model.thinking.defaultEffort ?? model.thinking.supportedEfforts?.[0] ?? ''
}

function defaultAIModel(availableModels: AIModel[]): AIModel | undefined {
  return availableModels.find((model) => model.id === DEFAULT_AI_MODEL_ID) ?? availableModels[0]
}

const thinkingEffort = computed<string>({
  get() {
    const model = currentModel.value
    if (!model?.thinking.supported) {
      return ''
    }

    return thinkingEffortPreferences.value[model.id] ?? defaultThinkingEffort(model)
  },
  set(value: string) {
    const model = currentModel.value
    if (!model?.thinking.supported) {
      return
    }

    thinkingEffortPreferences.value = {
      ...thinkingEffortPreferences.value,
      [model.id]: value,
    }
  },
})

const thinkingRequest = computed(() => {
  const model = currentModel.value
  if (!model?.thinking.supported) {
    return undefined
  }

  const request: { enabled: boolean; effort?: string } = {
    enabled: thinkingEnabled.value,
  }

  if (thinkingEnabled.value && canSelectThinkingEffort.value) {
    request.effort = thinkingEffort.value || defaultThinkingEffort(model)
  }

  return request
})

const slashCommandOptions = computed(() => {
  const value = inputValue.value.trim()
  if (!value.startsWith('/') || /\s/.test(value)) {
    return []
  }
  return slashCommands.filter((command) => command.name.startsWith(value))
})

function resolveSlashCommand(text: string): SlashCommand | undefined {
  const value = text.trim()
  return slashCommands.find((command) => command.name === value)
}

function applySlashCommand(command: SlashCommand) {
  inputValue.value = command.name
  void nextTick(() => {
    autoResize()
    inputEl.value?.focus()
  })
}

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

function thinkingEffortLabel(effort: string): string {
  switch (effort) {
    case 'none':
      return 'None'
    case 'low':
      return 'Low'
    case 'medium':
      return 'Medium'
    case 'high':
      return 'High'
    case 'xhigh':
      return 'Extra high'
    default:
      return effort
  }
}

function handleAgentSelectorChange(event: Event) {
  const agentId = (event.target as HTMLSelectElement).value
  aiAgentStore.setSelectedAgent(agentId)
  emit('select-agent', agentId)
}

onMounted(async () => {
  try {
    const res = await aiApi.listModels()
    models.value = res.models.filter((m) => m.configured)
    const defaultModel = defaultAIModel(models.value)
    if (defaultModel) {
      selectedModel.value = defaultModel.id
    }
  } catch {
    // models will remain empty
  }
  // Fetch conversation metadata for internal thread tracking (non-blocking).
  conversationStore.fetchConversations(props.workspaceId)
  aiAgentStore.fetchAgents(props.workspaceId)
})

onUnmounted(() => {
  cancelFocusedRunEventStream()
  if (scrollRaf !== null) cancelAnimationFrame(scrollRaf)
  if (fetchRunsDebounceTimer !== null) clearTimeout(fetchRunsDebounceTimer)
})

watch(() => props.focusedRunId, (runId) => {
  void loadFocusedRun(runId ?? null)
}, { immediate: true })

function upsertPlanSegment(segments: MessageSegment[], steps: PlanStep[]): void {
  if (steps.length === 0) return
  const existing = segments.find((seg): seg is PlanSegment => seg.type === 'plan')
  if (existing) {
    existing.steps = steps
  } else {
    segments.push({ type: 'plan', steps })
  }
}

function messageRenderItems(msg: DisplayMessage): MessageRenderItem[] {
  const items: MessageRenderItem[] = []
  let pendingTools: ToolSegment[] = []
  let pendingKey = ''

  const flushTools = () => {
    if (pendingTools.length === 0) return
    items.push({
      type: 'tool-group',
      key: pendingTools.map((tool) => tool.toolCallId).join(':'),
      tools: pendingTools,
    })
    pendingTools = []
    pendingKey = ''
  }

  for (const seg of msg.segments) {
    if (seg.type === 'tool' && shouldRenderToolCall(seg.name)) {
      const key = toolGroupKey(seg.name)
      if (pendingTools.length > 0 && pendingKey !== key) {
        flushTools()
      }
      pendingTools.push(seg)
      pendingKey = key
      continue
    }

    flushTools()
    if (seg.type !== 'tool') {
      items.push(seg)
    }
  }

  flushTools()
  return items
}

function messageMarkdown(msg: DisplayMessage): string {
  return msg.content
}

async function handleApproval(seg: ApprovalSegment, approved: boolean) {
  try {
    await aiApi.approveCommand(seg.id, approved)
    seg.status = approved ? 'approved' : 'denied'
    seg.error = undefined
  } catch (err) {
    seg.error = err instanceof Error ? err.message : 'Failed to resolve approval'
  }
}

function approvalStatusLabel(status: ApprovalSegment['status']): string {
  switch (status) {
    case 'approved':
      return 'Approved'
    case 'denied':
      return 'Denied'
    case 'expired':
      return 'Expired'
    case 'failed':
      return 'Failed'
    default:
      return 'Pending'
  }
}

function findApprovalSegment(segments: MessageSegment[], id: string): ApprovalSegment | undefined {
  return segments.find((seg): seg is ApprovalSegment => seg.type === 'approval' && seg.id === id)
}

function applyApprovalRequired(segments: MessageSegment[], id: string, command: string): void {
  const existing = findApprovalSegment(segments, id)
  if (existing) {
    existing.command = command
    return
  }

  segments.push({
    type: 'approval',
    id,
    command,
    status: 'pending',
  })
}

function applyApprovalResolved(segments: MessageSegment[], result: ApprovalResult): void {
  const existing = findApprovalSegment(segments, result.id)
  if (existing) {
    existing.command = result.command || existing.command
    existing.status = result.status
    existing.error = undefined
    return
  }

  segments.push({
    type: 'approval',
    id: result.id,
    command: result.command,
    status: result.status,
  })
}

function createQuestionSegment(request: UserQuestionRequest, result?: UserQuestionResult): QuestionSegment {
  const selections: Record<string, string[]> = {}
  const customAnswers: Record<string, string> = {}
  const answers = result?.answers ?? []

  for (const question of request.questions) {
    const answer = answers.find((candidate) => candidate.questionId === question.id)
    selections[question.id] = answer?.values ? [...answer.values] : []
    customAnswers[question.id] = answer?.custom ?? ''
  }

  return {
    type: 'question',
    request,
    status: result?.status ?? 'pending',
    answers,
    selections,
    customAnswers,
  }
}

function parseQuestionFromToolArgs(toolCallId: string, args: string, result?: string): QuestionSegment | null {
  try {
    const parsed = JSON.parse(args) as Record<string, unknown>
    const rawQuestions = Array.isArray(parsed.questions) ? parsed.questions : []
    if (rawQuestions.length === 0) return null
    const request: UserQuestionRequest = {
      id: toolCallId,
      title: typeof parsed.title === 'string' ? parsed.title : undefined,
      questions: rawQuestions.flatMap((question): UserQuestion[] => {
        if (!question || typeof question !== 'object' || Array.isArray(question)) return []
        const record = question as Record<string, unknown>
        const id = typeof record.id === 'string' ? record.id : ''
        const prompt = typeof record.prompt === 'string' ? record.prompt : ''
        if (!id || !prompt) return []
        const rawType = record.type
        const type = rawType === 'multiple_choice' || rawType === 'text' ? rawType : 'single_choice'
        const rawOptions = Array.isArray(record.options) ? record.options : []
        const options = rawOptions.flatMap((option): UserQuestionOption[] => {
          if (!option || typeof option !== 'object' || Array.isArray(option)) return []
          const optionRecord = option as Record<string, unknown>
          const value = typeof optionRecord.value === 'string' ? optionRecord.value : ''
          if (!value) return []
          const label = typeof optionRecord.label === 'string' && optionRecord.label ? optionRecord.label : value
          return [{ value, label }]
        })
        return [{
          id,
          prompt,
          type,
          options,
          allowCustom: record.allow_custom !== false,
          placeholder: typeof record.placeholder === 'string' ? record.placeholder : undefined,
        }]
      }),
    }
    if (request.questions.length === 0) return null

    let parsedResult: UserQuestionResult | undefined
    if (result) {
      try {
        const resultValue = JSON.parse(result) as UserQuestionResult
        if (resultValue && typeof resultValue === 'object' && resultValue.status) {
          parsedResult = resultValue
        }
      } catch {
        parsedResult = undefined
      }
    }
    return createQuestionSegment(request, parsedResult)
  } catch {
    return null
  }
}

function findQuestionSegment(segments: MessageSegment[], id: string): QuestionSegment | undefined {
  return segments.find((seg): seg is QuestionSegment => seg.type === 'question' && seg.request.id === id)
}

function applyQuestionRequired(segments: MessageSegment[], request: UserQuestionRequest): void {
  const existing = findQuestionSegment(segments, request.id)
  if (existing) {
    existing.request = request
    existing.status = 'pending'
    return
  }

  segments.push(createQuestionSegment(request))
}

function applyQuestionResolved(segments: MessageSegment[], result: UserQuestionResult): void {
  const existing = findQuestionSegment(segments, result.id)
  if (!existing) return

  existing.status = result.status
  existing.answers = result.answers ?? []
  existing.error = undefined
  for (const answer of existing.answers) {
    existing.selections[answer.questionId] = answer.values ? [...answer.values] : []
    existing.customAnswers[answer.questionId] = answer.custom ?? ''
  }
}

function markQuestionAnswered(seg: QuestionSegment, answers: UserQuestionAnswer[]): void {
  seg.status = 'answered'
  seg.answers = answers
  seg.error = undefined
  for (const answer of answers) {
    seg.selections[answer.questionId] = answer.values ? [...answer.values] : []
    seg.customAnswers[answer.questionId] = answer.custom ?? ''
  }
}

function markQuestionError(seg: QuestionSegment, message: string): void {
  seg.error = message
}

function formatTokenCount(tokens: number): string {
  if (tokens >= 1000) {
    return `${(tokens / 1000).toFixed(1).replace(/\.0$/, '')}k`
  }
  return `${tokens}`
}

function contextSizeLabel(size: ContextSize): string {
  if (size.inputBudgetTokens > 0) {
    return `~${formatTokenCount(size.nextRequestTokens)} / ${formatTokenCount(size.inputBudgetTokens)} tokens`
  }
  return `~${formatTokenCount(size.nextRequestTokens)} tokens`
}

function contextSizeTitle(size: ContextSize): string {
  const parts = [
    `Approximate provider input for the next request: ${size.nextRequestTokens.toLocaleString()} tokens.`,
    `Transcript before compaction: ${size.totalTranscriptTokens.toLocaleString()} tokens.`,
    `Provider-facing after compaction: ${size.providerFacingTokens.toLocaleString()} tokens.`,
  ]
  if (size.inputBudgetTokens > 0) {
    parts.push(`Budget used: ${size.percentageUsed.toFixed(1)}% of ${size.inputBudgetTokens.toLocaleString()} tokens.`)
  }
  return parts.join(' ')
}

function refreshRunForEvent(event: StreamEvent): void {
  if (!event.runId) return
  agentRunStore.refreshRun(event.runId).catch((err) => {
    console.error('Failed to refresh agent run:', err)
  })
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
        if (tc.function.name === 'update_plan') {
          upsertPlanSegment(display.segments, planStepsFromToolArgs(tc.function.arguments))
          continue
        }
        if (tc.function.name === 'ask_user') {
          const questionSegment = parseQuestionFromToolArgs(tc.id, tc.function.arguments, toolResults.get(tc.id))
          if (questionSegment) {
            display.segments.push(questionSegment)
          }
          continue
        }

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

function cloneDisplayMessages(source: DisplayMessage[]): DisplayMessage[] {
  return source.map((msg) => ({
    role: msg.role,
    content: msg.content,
    segments: msg.segments.map(cloneMessageSegment),
  }))
}

function cloneMessageSegment(seg: MessageSegment): MessageSegment {
  switch (seg.type) {
    case 'text':
      return { type: 'text', content: seg.content }
    case 'tool':
      return { type: 'tool', toolCallId: seg.toolCallId, name: seg.name, args: seg.args, result: seg.result }
    case 'approval':
      return { type: 'approval', id: seg.id, command: seg.command, status: seg.status, error: seg.error }
    case 'question':
      return {
        type: 'question',
        request: {
          ...seg.request,
          questions: seg.request.questions.map((question) => ({
            ...question,
            options: question.options?.map((option) => ({ ...option })),
          })),
        },
        status: seg.status,
        answers: seg.answers.map((answer) => ({ ...answer, values: answer.values ? [...answer.values] : undefined })),
        selections: Object.fromEntries(Object.entries(seg.selections).map(([key, values]) => [key, [...values]])),
        customAnswers: { ...seg.customAnswers },
        error: seg.error,
      }
    case 'plan':
      return { type: 'plan', steps: seg.steps.map((step) => ({ ...step })) }
    case 'thinking':
      return { type: 'thinking', content: seg.content }
  }
}

function cancelFocusedRunEventStream() {
  focusedRunEventController.value?.abort()
  focusedRunEventController.value = null
}

async function loadFocusedRun(runId: number | null) {
  cancelFocusedRunEventStream()
  focusedRunMessages.value = []
  focusedRunError.value = null
  focusedRunLastSequence.value = 0
  focusedRunOutputStarted.value = false
  focusedRunContextSize.value = null

  if (runId === null) {
    focusedRunLoading.value = false
    return
  }

  focusedRunLoading.value = true
  const loadedRun = await agentRunStore.refreshRun(runId)
  if (props.focusedRunId !== runId) {
    focusedRunLoading.value = false
    return
  }
  if (!loadedRun) {
    focusedRunLoading.value = false
    focusedRunError.value = agentRunStore.error
    return
  }
  focusedRunMessages.value = reconstructDisplayMessages(loadedRun.inputMessages ?? [])
  await nextTick()
  scrollToBottom({ force: true })

  const controller = new AbortController()
  focusedRunEventController.value = controller
  focusedRunLoading.value = false

  try {
    await aiApi.streamAgentRunEvents(
      runId,
      (event) => {
        applyFocusedRunEvent(event)
        focusedRunLastSequence.value = Math.max(focusedRunLastSequence.value, event.sequence ?? 0)
        void nextTick(() => scrollToBottom())
      },
      focusedRunLastSequence.value,
      controller.signal,
    )
  } catch (err) {
    if (!isAbortError(err)) {
      focusedRunError.value = err instanceof Error ? err.message : 'Failed to load agent run events'
    }
  } finally {
    if (focusedRunEventController.value === controller) {
      focusedRunEventController.value = null
    }
    if (props.workspaceId > 0) {
      void agentRunStore.fetchRuns(props.workspaceId)
    }
  }
}

function applyFocusedRunEvent(event: StreamEvent) {
  if (event.contextSize) {
    focusedRunContextSize.value = event.contextSize
  }

  if (!hasDisplayableRunEventContent(event)) {
    return
  }

  const msg = ensureFocusedRunAssistantMessage()

  if (event.error) {
    appendTextSegment(msg, `\n\nError: ${event.error}`)
  }

  if (event.reasoningContent) {
    const lastSeg = msg.segments[msg.segments.length - 1]
    if (lastSeg && lastSeg.type === 'thinking') {
      lastSeg.content += event.reasoningContent
    } else {
      msg.segments.push({ type: 'thinking', content: event.reasoningContent })
    }
  }

  if (event.content) {
    appendTextSegment(msg, event.content)
  }

  if (event.toolCalls) {
    for (const tc of event.toolCalls) {
      if (!shouldRenderToolCall(tc.name)) continue
      msg.segments.push({
        type: 'tool',
        toolCallId: tc.id,
        name: tc.name,
        args: tc.arguments,
      })
    }
  }

  const toolResult = event.toolResult
  if (toolResult) {
    const toolSeg = msg.segments.find(
      (seg): seg is ToolSegment => seg.type === 'tool' && seg.toolCallId === toolResult.toolCallId && !hasToolResult(seg),
    )
    if (toolSeg) {
      toolSeg.result = toolResult.content
    }
    refreshRunsAfterSubAgentTool(toolResult)
  }

  if (event.approvalRequired) {
    applyApprovalRequired(msg.segments, event.approvalRequired.id, event.approvalRequired.command)
    refreshRunForEvent(event)
  }

  if (event.approvalResolved) {
    applyApprovalResolved(msg.segments, event.approvalResolved)
    refreshRunForEvent(event)
  }

  if (event.questionRequired) {
    applyQuestionRequired(msg.segments, event.questionRequired)
    refreshRunForEvent(event)
  }

  if (event.questionResolved) {
    applyQuestionResolved(msg.segments, event.questionResolved)
    refreshRunForEvent(event)
  }

  if (event.plan) {
    upsertPlanSegment(msg.segments, event.plan)
  }
}

let fetchRunsDebounceTimer: number | null = null

function refreshRunsAfterSubAgentTool(toolResult: ToolResult) {
  if (toolResult.name !== 'spawn_sub_agent' || props.workspaceId <= 0) return
  if (fetchRunsDebounceTimer !== null) clearTimeout(fetchRunsDebounceTimer)
  fetchRunsDebounceTimer = window.setTimeout(() => {
    fetchRunsDebounceTimer = null
    void agentRunStore.fetchRuns(props.workspaceId)
  }, 300)
}

function hasDisplayableRunEventContent(event: StreamEvent): boolean {
  return Boolean(
    event.error ||
    event.reasoningContent ||
    event.content ||
    event.toolCalls?.length ||
    event.toolResult ||
    event.approvalRequired ||
    event.approvalResolved ||
    event.questionRequired ||
    event.questionResolved ||
    event.plan?.length,
  )
}

function ensureFocusedRunAssistantMessage(): DisplayMessage {
  const last = focusedRunMessages.value[focusedRunMessages.value.length - 1]
  if (focusedRunOutputStarted.value && last && last.role === 'assistant') {
    return last
  }

  const msg: DisplayMessage = { role: 'assistant', content: '', segments: [] }
  focusedRunMessages.value.push(msg)
  focusedRunOutputStarted.value = true
  return msg
}

function appendTextSegment(msg: DisplayMessage, content: string) {
  msg.content += content
  const lastSeg = msg.segments[msg.segments.length - 1]
  if (lastSeg && lastSeg.type === 'text') {
    lastSeg.content += content
  } else {
    msg.segments.push({ type: 'text', content })
  }
}

function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}

async function loadConversation(convId: number) {
  const res = await aiApi.getMessages(convId)
  rawMessages.value = res.messages
  messages.value = reconstructDisplayMessages(res.messages)
  activeConversationId.value = convId
  continuationParentRunId.value = findContinuationParentRunId(convId)
  conversationStore.setActive(convId)
  await nextTick()
  scrollToBottom({ force: true })
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

async function ensureActiveConversation(): Promise<number | null> {
  if (activeConversationId.value !== null) {
    return activeConversationId.value
  }

  try {
    const conv = await conversationStore.createConversation(props.workspaceId, selectedModel.value)
    activeConversationId.value = conv.id
    conversationStore.setActive(conv.id)
    return conv.id
  } catch (err) {
    console.error('Failed to create conversation for agent run:', err)
    return null
  }
}

async function sendMessage() {
  const text = inputValue.value.trim()
  if (!text || streaming.value) return
  const slashCommand = resolveSlashCommand(text)
  const displayText = slashCommand?.name ?? text
  const requestText = slashCommand?.prompt ?? text

  if (!selectedModel.value) {
    messages.value.push({
      role: 'assistant',
      content: 'No AI model is configured. Ask an admin to set up an AI provider in Settings.',
      segments: [{ type: 'text', content: 'No AI model is configured. Ask an admin to set up an AI provider in Settings.' }],
    })
    await nextTick()
    scrollToBottom({ force: true })
    return
  }

  const conversationId = await ensureActiveConversation()
  if (conversationId === null) {
    messages.value.push({
      role: 'assistant',
      content: 'Unable to start the AI agent because the chat session could not be created.',
      segments: [{ type: 'text', content: 'Unable to start the AI agent because the chat session could not be created.' }],
    })
    await nextTick()
    scrollToBottom({ force: true })
    return
  }

  // Push user message to display and raw arrays.
  messages.value.push({ role: 'user', content: displayText, segments: [] })
  rawMessages.value.push({ role: 'user', content: displayText })
  inputValue.value = ''
  resetInputHeight()

  // Add empty assistant message for streaming.
  messages.value.push({ role: 'assistant', content: '', segments: [] })
  const assistantIdx = messages.value.length - 1

  await nextTick()
  scrollToBottom({ force: true })

  streaming.value = true
  const controller = new AbortController()
  abortController.value = controller
  activeContextSize.value = null

  // Build the API payload from rawMessages (excludes the empty assistant placeholder).
  const chatMessages: ChatMessage[] = rawMessages.value.slice()
  if (slashCommand && chatMessages.length > 0) {
    chatMessages[chatMessages.length - 1] = { role: 'user', content: requestText }
  }

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
  let streamDetached = false
  let createdRun: AgentRun | null = null
  const parentRunId = continuationParentRunId.value

  try {
    const created = await aiApi.createAgentRun(
      selectedModel.value,
      chatMessages,
      props.workspaceId,
      conversationId,
      thinkingRequest.value,
      parentRunId ?? undefined,
      props.selectedAgentId,
    )
    createdRun = created.run
    agentRunStore.upsertRun(created.run)
    activeAgentRunId.value = created.run.id

    await aiApi.streamAgentRunEvents(
      created.run.id,
      (event: StreamEvent) => {
        if (event.contextSize) {
          activeContextSize.value = event.contextSize
        }

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
            if (!shouldRenderToolCall(tc.name)) continue
            messages.value[assistantIdx].segments.push({
              type: 'tool',
              toolCallId: tc.id,
              name: tc.name,
              args: tc.arguments,
            })
          }
        }

        const toolResult = event.toolResult
        if (toolResult) {
          const segs = messages.value[assistantIdx].segments
          const toolSeg = segs.find(
            (s): s is ToolSegment => s.type === 'tool' && s.toolCallId === toolResult.toolCallId && !hasToolResult(s)
          )
          if (toolSeg) {
            toolSeg.result = toolResult.content
          }
          rounds[rounds.length - 1].toolResults.push({
            role: 'tool',
            content: toolResult.content,
            tool_call_id: toolResult.toolCallId,
          })
          refreshRunsAfterSubAgentTool(toolResult)
        }

        if (event.approvalRequired) {
          applyApprovalRequired(messages.value[assistantIdx].segments, event.approvalRequired.id, event.approvalRequired.command)
          refreshRunForEvent(event)
        }

        if (event.approvalResolved) {
          applyApprovalResolved(messages.value[assistantIdx].segments, event.approvalResolved)
          refreshRunForEvent(event)
        }

        if (event.questionRequired) {
          applyQuestionRequired(messages.value[assistantIdx].segments, event.questionRequired)
          refreshRunForEvent(event)
        }

        if (event.questionResolved) {
          applyQuestionResolved(messages.value[assistantIdx].segments, event.questionResolved)
          refreshRunForEvent(event)
        }

        if (event.plan) {
          upsertPlanSegment(messages.value[assistantIdx].segments, event.plan)
        }
        scrollToBottom()
      },
      0,
      controller.signal,
    )
  } catch (err: any) {
    streamFailed = true
    // Aborting only detaches this panel from the SSE stream — newChat() has already reset
    // the panel and the run keeps going server-side, so this turn owns no state to persist.
    streamDetached = isAbortError(err)
    if (!streamDetached) {
      messages.value[assistantIdx].content =
        messages.value[assistantIdx].content || `Error: ${err.message}`
    }
  } finally {
    // Flush each LLM round to rawMessages: assistant message followed by its tool results.
    // This preserves the exact interleaved structure the provider saw across iterations.
    // Failed streams flush too: dropping the exchange would erase the user's message along
    // with everything the agent already did, so the next request would start with no history
    // even though the panel still shows the conversation.
    if (!streamDetached) {
      for (const round of rounds) {
        // A round interrupted mid-iteration can hold tool calls that never ran. Providers
        // reject tool calls without matching results, so only keep the answered ones.
        const toolCalls = streamFailed
          ? round.toolCalls.filter((call) => round.toolResults.some((result) => result.tool_call_id === call.id))
          : round.toolCalls
        if (streamFailed && !round.content && !round.reasoningContent && toolCalls.length === 0) continue

        const assistantRaw: ChatMessage = { role: 'assistant', content: round.content }
        if (round.reasoningContent) assistantRaw.reasoning_content = round.reasoningContent
        if (round.thinkingState !== undefined) assistantRaw.thinking_state = round.thinkingState
        if (toolCalls.length > 0) assistantRaw.tool_calls = toolCalls
        rawMessages.value.push(assistantRaw)
        for (const toolMsg of round.toolResults) rawMessages.value.push(toolMsg)
      }
    }

    streaming.value = false
    abortController.value = null
    const completedRunId = activeAgentRunId.value
    activeAgentRunId.value = null
    await nextTick()
    scrollToBottom()

    // Auto-save whatever the turn produced, including after a failure — a transient
    // provider error must not cost the user their history. Await it before refreshing the
    // run status so that the "Continue" button only becomes available once persisted.
    if (!streamDetached) {
      await saveCurrentConversation().catch((err) => {
        console.error('Auto-save failed:', err)
      })
      if (createdRun) {
        continuationParentRunId.value = rootRunIdFor(createdRun)
      }
    }

    if (completedRunId !== null) {
      agentRunStore.refreshRun(completedRunId).catch((err) => {
        console.error('Failed to refresh completed agent run:', err)
      })
    }
  }
}

function stopStreaming() {
  if (activeAgentRunId.value !== null) {
    aiApi.cancelAgentRun(activeAgentRunId.value).catch((err) => {
      console.error('Failed to cancel agent run:', err)
      abortController.value?.abort()
    })
    return
  }

  abortController.value?.abort()
}

function newChat() {
  if (streaming.value) {
    // Disconnect from the SSE stream without cancelling the backend run.
    // The run continues in the background and remains visible in AiAgentRunsPanel.
    // Use stopStreaming() to explicitly cancel a run.
    abortController.value?.abort()
  }
  messages.value = []
  rawMessages.value = []
  inputValue.value = ''
  planExpanded.value = false
  activeAgentRunId.value = null
  continuationParentRunId.value = null
  activeContextSize.value = null
  focusedRunContextSize.value = null
  autoScrollEnabled.value = true
  resetInputHeight()
  activeConversationId.value = null
  conversationStore.setActive(null)
}

function findContinuationParentRunId(conversationId: number): number | null {
  const runs = agentRunStore.runs
    .filter((run) => run.workspaceId === props.workspaceId && run.conversationId === conversationId)
    .sort(compareRunsByUpdatedAt)
  const latest = runs[0]
  return latest ? rootRunIdFor(latest) : null
}

function rootRunIdFor(run: AgentRun): number {
  let current = run
  const seen = new Set<number>()

  while (current.parentRunId && !seen.has(current.id)) {
    seen.add(current.id)
    const parent = agentRunStore.runs.find((candidate) => candidate.id === current.parentRunId)
    if (!parent) return current.parentRunId
    current = parent
  }

  return current.id
}

function compareRunsByUpdatedAt(a: AgentRun, b: AgentRun): number {
  return runTime(b.updatedAt, b.createdAt) - runTime(a.updatedAt, a.createdAt) || b.id - a.id
}

function runTime(updatedAt: string, createdAt: string): number {
  const updated = new Date(updatedAt).getTime()
  if (Number.isFinite(updated)) return updated
  const created = new Date(createdAt).getTime()
  return Number.isFinite(created) ? created : 0
}

async function continueFocusedRunConversation() {
  const conversationId = focusedRun.value?.conversationId
  if (!conversationId) return

  try {
    const parentRunId = rootRunIdFor(focusedRun.value)
    const focusedTranscript = cloneDisplayMessages(focusedRunMessages.value)
    await loadConversation(conversationId)
    continuationParentRunId.value = parentRunId
    if (focusedTranscript.length > 0) {
      messages.value = focusedTranscript
    }
    emit('clear-focused-run')
    await nextTick()
    scrollToBottom({ force: true })
    inputEl.value?.focus()
  } catch (err) {
    focusedRunError.value = err instanceof Error ? err.message : 'Failed to load conversation for this run'
  }
}

const displayIsThinking = computed(() => {
  if (!displayStreaming.value) return false
  const last = visibleMessages.value[visibleMessages.value.length - 1]
  if (!last || last.role !== 'assistant') return false
  return last.segments.length === 0
})

// activityStatus describes what the agent is currently doing during streaming.
const displayActivityStatus = computed<string | null>(() => {
  if (!displayStreaming.value) return null
  const last = visibleMessages.value[visibleMessages.value.length - 1]
  if (!last || last.role !== 'assistant') return null
  if (last.segments.length === 0) return null // isThinking handles this case

  const segs = last.segments
  const lastSeg = segs[segs.length - 1]

  // A tool is running (no result yet)
  if (lastSeg.type === 'tool' && !hasToolResult(lastSeg as ToolSegment)) {
    return `Running ${lastSeg.name}…`
  }

  // Waiting for user approval
  if (lastSeg.type === 'approval' && (lastSeg as ApprovalSegment).status === 'pending') {
    return 'Waiting for approval…'
  }

  if (lastSeg.type === 'question' && (lastSeg as QuestionSegment).status === 'pending') {
    return 'Waiting for your answer…'
  }

  // Last segment is a completed tool or has a result — the LLM is generating the next response
  if (lastSeg.type === 'tool' && hasToolResult(lastSeg as ToolSegment)) {
    return 'Thinking…'
  }

  // Last segment is text but we're still streaming — LLM is still writing
  // No status needed since the user can see text arriving
  return null
})

// Find the latest plan across all messages.
const activePlan = computed<PlanStep[] | null>(() => {
  for (let i = visibleMessages.value.length - 1; i >= 0; i--) {
    const msg = visibleMessages.value[i]
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

let scrollRaf: number | null = null
let scrollRafForce = false
const AUTO_SCROLL_THRESHOLD_PX = 48

function isNearChatBottom(el: HTMLElement): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight <= AUTO_SCROLL_THRESHOLD_PX
}

function handleChatScroll() {
  const el = chatBody.value
  if (!el) return
  autoScrollEnabled.value = isNearChatBottom(el)
}

function scrollToBottom(options: { force?: boolean } = {}) {
  const force = options.force === true
  if (!force && !autoScrollEnabled.value) return
  scrollRafForce = scrollRafForce || force
  if (scrollRaf !== null) return
  scrollRaf = requestAnimationFrame(() => {
    scrollRaf = null
    const shouldForce = scrollRafForce
    scrollRafForce = false
    const el = chatBody.value
    if (el && (shouldForce || autoScrollEnabled.value)) {
      el.scrollTop = el.scrollHeight
      autoScrollEnabled.value = true
    }
  })
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
        <span class="agent-title">{{ focusedRunTitle }}</span>
      </div>
      <div v-if="viewingFocusedRun" class="agent-header-actions">
        <span class="focused-run-status" :class="{ active: focusedRunActive }">
          <span class="focused-run-dot" />
          {{ focusedRunStatus }}
        </span>
        <span
          v-if="displayedContextSize"
          class="context-size-badge"
          :class="{ warning: displayedContextSize.warning }"
          :title="contextSizeTitle(displayedContextSize)"
        >
          {{ contextSizeLabel(displayedContextSize) }}
        </span>
        <button
          class="continue-run-btn"
          type="button"
          :disabled="!canContinueFocusedRun"
          title="Continue this conversation"
          @click="continueFocusedRunConversation"
        >
          Continue
        </button>
        <button class="header-icon-btn" title="Return to chat" @click="emit('clear-focused-run')">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="m12 19-7-7 7-7" />
            <path d="M19 12H5" />
          </svg>
        </button>
      </div>
      <div v-else class="agent-header-actions">
        <span
          v-if="displayedContextSize"
          class="context-size-badge"
          :class="{ warning: displayedContextSize.warning }"
          :title="contextSizeTitle(displayedContextSize)"
        >
          {{ contextSizeLabel(displayedContextSize) }}
        </span>
        <button class="new-chat-btn" title="New Chat" @click="newChat">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 5v14" />
            <path d="M5 12h14" />
          </svg>
        </button>
      </div>
    </div>

    <div ref="chatBody" class="agent-body" @scroll="handleChatScroll">
      <div v-if="viewingFocusedRun" class="run-focus-banner">
        <span class="run-focus-label">Focused background run</span>
        <span v-if="focusedRun?.model" class="run-focus-meta">{{ focusedRun.model }}</span>
      </div>
      <div v-if="focusedRunError" class="run-focus-error">
        {{ focusedRunError }}
      </div>
      <div v-if="focusedRunLoading" class="chat-empty">
        <p class="empty-text">Loading agent run…</p>
      </div>
      <div v-else-if="visibleMessages.length === 0" class="chat-empty">
        <div class="empty-icon">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="m12 3-1.912 5.813a2 2 0 0 1-1.275 1.275L3 12l5.813 1.912a2 2 0 0 1 1.275 1.275L12 21l1.912-5.813a2 2 0 0 1 1.275-1.275L21 12l-5.813-1.912a2 2 0 0 1-1.275-1.275L12 3Z" />
            <path d="M5 3v4" />
            <path d="M19 17v4" />
            <path d="M3 5h4" />
            <path d="M17 19h4" />
          </svg>
        </div>
        <p class="empty-text">
          {{ viewingFocusedRun ? 'No events have been recorded for this run yet.' : 'Ask me anything about your code.' }}
        </p>
      </div>
      <div
        v-for="(msg, i) in visibleMessages"
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
          :class="{ 'has-copy-action': messageMarkdown(msg) }"
        >
          <AiMessageCopyButton v-if="messageMarkdown(msg)" :markdown="messageMarkdown(msg)" />
          <template v-for="(item, si) in messageRenderItems(msg)" :key="`${item.type}-${si}`">
            <AiThinkingSection v-if="item.type === 'thinking'" :content="(item as ThinkingSegment).content" />
            <AiToolGroup v-else-if="item.type === 'tool-group'" :group="item as ToolGroupDisplay" />
            <div v-else-if="item.type === 'approval'" class="approval-prompt">
              <div class="approval-header">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10" />
                </svg>
                <span class="approval-title">sudo command requires approval</span>
              </div>
              <code class="approval-command">{{ (item as ApprovalSegment).command }}</code>
              <div v-if="(item as ApprovalSegment).status === 'pending'" class="approval-actions">
                <button class="approval-btn approve" @click="handleApproval(item as ApprovalSegment, true)">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                  Approve
                </button>
                <button class="approval-btn deny" @click="handleApproval(item as ApprovalSegment, false)">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <line x1="18" y1="6" x2="6" y2="18" />
                    <line x1="6" y1="6" x2="18" y2="18" />
                  </svg>
                  Deny
                </button>
              </div>
              <div v-else class="approval-resolved">
                <span
                  v-if="(item as ApprovalSegment).status === 'approved'"
                  class="approval-badge approved"
                >
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                  {{ approvalStatusLabel((item as ApprovalSegment).status) }}
                </span>
                <span
                  v-else
                  class="approval-badge denied"
                  :class="{ expired: (item as ApprovalSegment).status === 'expired', failed: (item as ApprovalSegment).status === 'failed' }"
                >
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                    <line x1="18" y1="6" x2="6" y2="18" />
                    <line x1="6" y1="6" x2="18" y2="18" />
                  </svg>
                  {{ approvalStatusLabel((item as ApprovalSegment).status) }}
                </span>
              </div>
              <div v-if="(item as ApprovalSegment).error" class="approval-error">
                {{ (item as ApprovalSegment).error }}
              </div>
            </div>
            <AiQuestionCard
              v-else-if="item.type === 'question'"
              :request="(item as QuestionSegment).request"
              :status="(item as QuestionSegment).status"
              :answers="(item as QuestionSegment).answers"
              :error="(item as QuestionSegment).error"
              @answered="markQuestionAnswered(item as QuestionSegment, $event)"
              @error="markQuestionError(item as QuestionSegment, $event)"
            />
            <template v-else-if="item.type === 'plan'" />
            <MarkdownMessage v-else-if="item.type === 'text' && item.content" :content="item.content" />
          </template>
          <div v-if="i === visibleMessages.length - 1 && displayIsThinking" class="thinking-indicator">
            <span class="thinking-dot" />
            <span class="thinking-dot" />
            <span class="thinking-dot" />
          </div>
          <div v-else-if="i === visibleMessages.length - 1 && displayActivityStatus" class="activity-status">
            <span class="activity-spinner" />
            <span class="activity-label">{{ displayActivityStatus }}</span>
          </div>
        </div>
        <div
          v-else
          class="msg-content"
          :class="{ 'has-copy-action': messageMarkdown(msg) }"
        >
          <AiMessageCopyButton v-if="messageMarkdown(msg)" :markdown="messageMarkdown(msg)" />
          <span class="msg-plain-text">{{ msg.content }}</span>
        </div>
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

    <div v-if="viewingFocusedRun" class="run-focus-footer">
      <button
        class="continue-run-btn continue-run-btn--footer"
        type="button"
        :disabled="!canContinueFocusedRun"
        @click="continueFocusedRunConversation"
      >
        Continue conversation
      </button>
      <button class="return-chat-btn" type="button" @click="emit('clear-focused-run')">
        Return to chat
      </button>
    </div>

    <div v-else class="agent-input-area">
      <div class="input-shell" :class="{ focused: inputFocused }">
        <div v-if="slashCommandOptions.length > 0" class="slash-command-menu">
          <button
            v-for="command in slashCommandOptions"
            :key="command.name"
            type="button"
            class="slash-command-option"
            @mousedown.prevent="applySlashCommand(command)"
          >
            <span class="slash-command-name">{{ command.name }}</span>
            <span class="slash-command-description">{{ command.description }}</span>
          </button>
        </div>
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
            <select
              :value="props.selectedAgentId"
              class="model-selector agent-selector"
              title="Select AI agent"
              @change="handleAgentSelectorChange"
            >
              <option v-for="agent in aiAgentStore.agents" :key="agent.id" :value="agent.id">
                {{ agent.name }}
              </option>
            </select>
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
            <select
              v-if="canSelectThinkingEffort && thinkingEnabled"
              v-model="thinkingEffort"
              class="model-selector thinking-effort-selector"
              title="Select reasoning level"
            >
              <option v-for="effort in currentModel?.thinking.supportedEfforts ?? []" :key="effort" :value="effort">
                {{ thinkingEffortLabel(effort) }}
              </option>
            </select>
          </template>
          <span v-else class="agent-badge">No Models</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.agent-panel {
  --ide-header-icon: var(--accent);
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--ide-surface-bg);
}

.agent-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  background: var(--ide-header-bg);
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
  color: var(--ide-header-icon);
}

.agent-title {
  font-size: var(--ide-header-title-size);
  font-weight: 600;
  color: var(--text-primary);
}

.agent-header-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2, 8px);
}

.focused-run-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 7px;
  border: 0.5px solid var(--border-default);
  border-radius: 999px;
  color: var(--text-muted);
  font-size: 0.68rem;
  text-transform: capitalize;
}

.focused-run-status.active {
  border-color: var(--success-border);
  background: var(--success-bg);
  color: var(--accent-green);
}

.focused-run-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--text-muted);
}

.focused-run-status.active .focused-run-dot {
  background: var(--accent-green);
  box-shadow: 0 0 8px var(--accent-green);
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

.context-size-badge {
  display: inline-flex;
  align-items: center;
  min-height: 23px;
  padding: 1px 7px;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-raised);
  color: var(--text-tertiary);
  font-family: var(--font-mono);
  font-size: 10.5px;
  font-weight: 500;
  white-space: nowrap;
}

.context-size-badge.warning {
  border-color: var(--warning-border);
  background: var(--warning-bg);
  color: var(--accent-amber);
}

.new-chat-btn,
.header-icon-btn {
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
.header-icon-btn:hover {
  background: var(--bg-raised);
  color: var(--text-primary);
  border-color: var(--border-strong);
}

.continue-run-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 22px;
  padding: 0 var(--space-2);
  border: 0.5px solid var(--accent-border);
  border-radius: var(--radius-md);
  background: var(--accent-glow);
  color: var(--accent);
  font-size: 0.72rem;
  font-weight: 600;
  transition: all var(--transition-fast);
}

.continue-run-btn:hover:not(:disabled) {
  background: var(--bg-raised);
  color: var(--text-primary);
  border-color: var(--border-strong);
}

.continue-run-btn:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.header-icon-btn.active {
  background: var(--accent-glow);
  color: var(--accent);
  border-color: var(--accent-border);
}

.model-selector {
  font-size: 0.75rem;
  min-height: 23px;
  padding: 2px 26px 2px 8px;
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--bg-raised) 70%, var(--bg-elevated));
  border: 0.5px solid var(--border-default);
  color: var(--text-secondary);
  outline: none;
  cursor: pointer;
  transition: border-color var(--transition-fast), color var(--transition-fast), background var(--transition-fast);
}

.thinking-effort-selector {
  min-width: 126px;
}

.model-selector:focus {
  border-color: var(--accent-border);
  color: var(--text-primary);
}

.thinking-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 23px;
  padding: 2px 8px;
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--bg-raised) 70%, var(--bg-elevated));
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
  background: var(--ide-surface-bg);
}

.run-focus-banner,
.run-focus-error {
  flex-shrink: 0;
  border-radius: var(--radius-md);
  font-size: 0.75rem;
}

.run-focus-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: 0.5px solid var(--accent-border);
  background: var(--accent-glow);
}

.run-focus-label {
  color: var(--text-primary);
  font-weight: 600;
}

.run-focus-meta {
  overflow: hidden;
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.7rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.run-focus-error {
  padding: var(--space-2) var(--space-3);
  border: 0.5px solid var(--error-border);
  background: var(--error-bg);
  color: var(--accent-rose);
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
  background: var(--bg-raised);
  color: var(--text-secondary);
  border: 0.5px solid var(--border-subtle);
}

.msg-content {
  flex: 1;
  position: relative;
  font-size: 0.8rem;
  line-height: 1.55;
  color: var(--text-secondary);
  padding: 10px 12px;
  border-radius: var(--radius-lg);
  min-width: 0;
  border: 0.5px solid transparent;
}

.msg-content.has-copy-action {
  padding-right: 40px;
}

.msg-copy-btn {
  position: absolute;
  top: 7px;
  right: 7px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--bg-raised) 82%, transparent);
  color: var(--text-muted);
  opacity: 0;
  transition: opacity var(--transition-fast), border-color var(--transition-fast), background var(--transition-fast), color var(--transition-fast);
}

.chat-message:hover .msg-copy-btn,
.msg-copy-btn:focus-visible,
.msg-copy-btn.copied,
.msg-copy-btn.failed {
  opacity: 1;
}

.msg-copy-btn:hover {
  border-color: var(--border-strong);
  background: var(--bg-hover);
  color: var(--text-primary);
}

.msg-copy-btn.copied {
  border-color: var(--success-border);
  background: var(--success-bg);
  color: var(--accent-green);
}

.msg-copy-btn.failed {
  border-color: var(--error-border);
  background: var(--error-bg);
  color: var(--accent-rose);
}

.msg-plain-text {
  display: block;
  white-space: pre-wrap;
}

.msg-assistant .msg-content {
  background: var(--bg-elevated);
  border-color: var(--border-hairline);
}

.msg-user .msg-content {
  background: var(--bg-raised);
  color: var(--text-primary);
  border-color: var(--border-subtle);
}

.agent-input-area {
  padding: var(--space-3);
  border-top: 0.5px solid var(--border-default);
  flex-shrink: 0;
  background: var(--ide-surface-bg);
}

.run-focus-footer {
  display: flex;
  justify-content: space-between;
  gap: var(--space-2);
  flex-shrink: 0;
  padding: var(--space-3);
  border-top: 0.5px solid var(--border-default);
}

.continue-run-btn--footer {
  min-height: auto;
  padding: var(--space-2) var(--space-3);
  font-size: 0.78rem;
}

.return-chat-btn {
  padding: var(--space-2) var(--space-3);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  font-size: 0.78rem;
  transition: all var(--transition-fast);
}

.return-chat-btn:hover {
  border-color: var(--border-active);
  background: var(--bg-hover);
  color: var(--text-primary);
}

.input-shell {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-strong);
  border-radius: var(--radius-xl);
  transition: border-color var(--transition-fast), box-shadow var(--transition-fast), background var(--transition-fast);
}

.input-shell.focused {
  border-color: var(--accent-border);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.slash-command-menu {
  display: flex;
  flex-direction: column;
  padding: var(--space-2);
  border-bottom: 0.5px solid var(--border-hairline);
  background: color-mix(in srgb, var(--bg-void) 40%, transparent);
}

.slash-command-option {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  padding: var(--space-2);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  text-align: left;
  transition: background var(--transition-fast), color var(--transition-fast);
}

.slash-command-option:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.slash-command-name {
  flex-shrink: 0;
  font-family: var(--font-mono);
  font-size: 0.78rem;
  color: var(--accent);
}

.slash-command-description {
  overflow: hidden;
  font-size: 0.78rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.input-container {
  display: flex;
  align-items: flex-end;
  gap: var(--space-3);
  min-height: 54px;
  padding: 12px;
  cursor: text;
}

.input-toolbar {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
  min-height: 34px;
  padding: 7px 10px;
  border-top: 0.5px solid var(--border-hairline);
  background: color-mix(in srgb, var(--bg-void) 34%, transparent);
}

.agent-input {
  flex: 1;
  background: transparent;
  border: none;
  font-family: var(--font-sans);
  font-size: 0.84rem;
  color: var(--text-primary);
  resize: none;
  outline: none;
  line-height: 1.55;
  max-height: 120px;
  min-height: 26px;
  padding: 2px 0;
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
  width: 32px;
  height: 32px;
  border-radius: var(--radius-md);
  background: var(--bg-hover);
  color: var(--text-tertiary);
  flex-shrink: 0;
  transition: transform var(--transition-fast), border-color var(--transition-fast), background var(--transition-fast), color var(--transition-fast), opacity var(--transition-fast);
  cursor: pointer;
  border: 0.5px solid var(--border-subtle);
}

.agent-send.active {
  background: var(--accent);
  color: var(--bg-base);
  border-color: var(--accent-border);
}

.agent-send.active:hover {
  transform: translateY(-1px);
}

.agent-stop {
  background: var(--warning) !important;
  color: var(--bg-base) !important;
  border-color: var(--warning-border) !important;
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

.approval-badge.expired,
.approval-badge.failed {
  background: var(--warning-bg);
  color: var(--accent-amber);
}

.approval-error {
  margin-top: var(--space-2);
  color: var(--accent-rose);
  font-size: 0.72rem;
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
