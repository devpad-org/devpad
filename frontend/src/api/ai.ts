import { apiClient } from './client'

export interface Conversation {
  id: number
  userId: number
  workspaceId: number
  title: string
  model: string
  createdAt: string
  updatedAt: string
}

export interface AIModel {
  id: string
  name: string
  providerId: string
  providerName: string
  configured: boolean
  thinking: {
    supported: boolean
    enabledByDefault: boolean
    canDisable: boolean
  }
}

export interface ThinkingConfig {
	enabled?: boolean
}

export type AgentRunStatus = 'queued' | 'running' | 'waiting_approval' | 'completed' | 'failed' | 'cancelled'

export interface AgentRun {
  id: number
  parentRunId?: number
  userId: number
  workspaceId: number
  conversationId?: number
  promptPreview?: string
  model: string
  status: AgentRunStatus
  error?: string
  inputMessages?: ChatMessage[]
  createdAt: string
  updatedAt: string
  startedAt?: string
  completedAt?: string
}

export interface AIProvider {
  id: string
  name: string
  enabled: boolean
  hasApiKey: boolean
}

export interface ChatMessage {
  role: 'user' | 'assistant' | 'system' | 'tool'
  content: string
  reasoning_content?: string
  thinking_state?: unknown
  tool_calls?: ToolCall[]
  tool_call_id?: string
}

export interface ToolCall {
  id: string
  itemId?: string
  type: string
  function: {
    name: string
    arguments: string
  }
}

export interface ToolResult {
  toolCallId: string
  name: string
  content: string
}

export interface ApprovalRequest {
  id: string
  command: string
}

export interface ApprovalResult {
  id: string
  command: string
  status: 'approved' | 'denied' | 'expired' | 'failed'
}

export interface PlanStep {
  title: string
  status: 'pending' | 'in_progress' | 'completed' | 'failed'
}

export interface StreamToolCall {
  id: string
  itemId?: string
  type: string
  name: string
  arguments: string
}

export interface StreamEvent {
  runId?: number
  sequence?: number
  reasoningContent?: string
  thinkingState?: unknown
  content?: string
  toolCalls?: StreamToolCall[]
  toolResult?: ToolResult
  approvalRequired?: ApprovalRequest
  approvalResolved?: ApprovalResult
  plan?: PlanStep[]
  done?: boolean
  error?: string
}

// Internal transport types matching the backend's DTO schema.
interface PartDTO {
  kind: string
  text?: string
  thinking?: { text?: string; state?: unknown }
  toolCall?: { id: string; itemId?: string; name: string; arguments: string }
  toolResult?: { toolCallId: string; name: string; content: string; isError?: boolean }
}

interface TurnDTO {
  role: string
  parts: PartDTO[]
}

interface AgentRunDTO {
  id: number
  parentRunId?: number
  userId: number
  workspaceId: number
  conversationId?: number
  promptPreview?: string
  model: string
  status: AgentRunStatus
  error?: string
  inputTurns?: TurnDTO[]
  createdAt: string
  updatedAt: string
  startedAt?: string
  completedAt?: string
}

function messagesToTurns(messages: ChatMessage[]): TurnDTO[] {
  const turns: TurnDTO[] = []
  for (const msg of messages) {
    if (msg.role === 'tool') {
      turns.push({
        role: 'user',
        parts: [{
          kind: 'tool_result',
          toolResult: {
            toolCallId: msg.tool_call_id ?? '',
            name: '',
            content: msg.content,
          },
        }],
      })
    } else if (msg.role === 'assistant') {
      const parts: PartDTO[] = []
      if (msg.reasoning_content) {
        parts.push({
          kind: 'thinking',
          thinking: { text: msg.reasoning_content, state: msg.thinking_state },
        })
      }
      if (msg.content) {
        parts.push({ kind: 'text', text: msg.content })
      }
      for (const tc of msg.tool_calls ?? []) {
        parts.push({
          kind: 'tool_call',
          toolCall: { id: tc.id, itemId: tc.itemId, name: tc.function.name, arguments: tc.function.arguments },
        })
      }
      turns.push({ role: 'assistant', parts })
    } else {
      turns.push({
        role: msg.role,
        parts: msg.content ? [{ kind: 'text', text: msg.content }] : [],
      })
    }
  }
  return turns
}

function turnsToMessages(turns: TurnDTO[]): ChatMessage[] {
  const messages: ChatMessage[] = []
  for (const turn of turns) {
    if (turn.role === 'assistant') {
      const msg: ChatMessage = { role: 'assistant', content: '' }
      const toolCalls: ToolCall[] = []
      for (const part of turn.parts) {
        if (part.kind === 'text') {
          msg.content += part.text ?? ''
        } else if (part.kind === 'thinking' && part.thinking) {
          msg.reasoning_content = (msg.reasoning_content ?? '') + (part.thinking.text ?? '')
          if (part.thinking.state !== undefined) {
            msg.thinking_state = part.thinking.state
          }
        } else if (part.kind === 'tool_call' && part.toolCall) {
          toolCalls.push({
            id: part.toolCall.id,
            itemId: part.toolCall.itemId,
            type: 'function',
            function: { name: part.toolCall.name, arguments: part.toolCall.arguments },
          })
        }
      }
      if (toolCalls.length > 0) msg.tool_calls = toolCalls
      messages.push(msg)
    } else if (turn.role === 'user') {
      let userContent = ''
      for (const part of turn.parts) {
        if (part.kind === 'text') {
          userContent += part.text ?? ''
        } else if (part.kind === 'tool_result' && part.toolResult) {
          messages.push({
            role: 'tool',
            content: part.toolResult.content,
            tool_call_id: part.toolResult.toolCallId,
          })
        }
      }
      if (userContent) {
        messages.push({ role: 'user', content: userContent })
      }
    } else {
      const text = turn.parts.filter(p => p.kind === 'text').map(p => p.text ?? '').join('')
      messages.push({ role: turn.role as ChatMessage['role'], content: text })
    }
  }
  return messages
}

function agentRunFromDTO(dto: AgentRunDTO): AgentRun {
  const run: AgentRun = {
    id: dto.id,
    parentRunId: dto.parentRunId,
    userId: dto.userId,
    workspaceId: dto.workspaceId,
    conversationId: dto.conversationId,
    promptPreview: dto.promptPreview,
    model: dto.model,
    status: dto.status,
    error: dto.error,
    createdAt: dto.createdAt,
    updatedAt: dto.updatedAt,
    startedAt: dto.startedAt,
    completedAt: dto.completedAt,
  }
  if (dto.inputTurns && dto.inputTurns.length > 0) {
    run.inputMessages = turnsToMessages(dto.inputTurns)
  }
  return run
}

export const aiApi = {
  listModels(): Promise<{ models: AIModel[] }> {
    return apiClient.get<{ models: AIModel[] }>('/api/ai/models')
  },

  listProviders(): Promise<{ providers: AIProvider[] }> {
    return apiClient.get<{ providers: AIProvider[] }>('/api/ai/providers')
  },

  updateProvider(id: string, apiKey: string, enabled: boolean): Promise<void> {
    return apiClient.put<void>(`/api/ai/providers/${id}`, { apiKey, enabled })
  },

  async chatStream(
    model: string,
    messages: ChatMessage[],
    onEvent: (event: StreamEvent) => void,
    signal?: AbortSignal,
    thinking?: ThinkingConfig,
  ): Promise<void> {
    const res = await fetch('/api/ai/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ model, turns: messagesToTurns(messages), thinking }),
      signal,
    })

    if (!res.ok) {
      const err = await res.json()
      throw new Error(err.error || `Chat request failed: ${res.status}`)
    }

    await readSSEStream(res, onEvent)
  },

  approveCommand(id: string, approved: boolean): Promise<void> {
    return apiClient.post<void>('/api/ai/agent/approve', { id, approved })
  },

  async listAgentRuns(workspaceId: number): Promise<{ runs: AgentRun[] }> {
    const res = await apiClient.get<{ runs: AgentRunDTO[] }>(`/api/ai/agent/runs?workspaceId=${workspaceId}`)
    return { runs: res.runs.map(agentRunFromDTO) }
  },

  async getAgentRun(id: number): Promise<{ run: AgentRun }> {
    const res = await apiClient.get<{ run: AgentRunDTO }>(`/api/ai/agent/runs/${id}`)
    return { run: agentRunFromDTO(res.run) }
  },

  async createAgentRun(
    model: string,
    messages: ChatMessage[],
    workspaceId: number,
    conversationId?: number,
    thinking?: ThinkingConfig,
    parentRunId?: number,
  ): Promise<{ run: AgentRun }> {
    const res = await apiClient.post<{ run: AgentRunDTO }>('/api/ai/agent/runs', {
      model,
      turns: messagesToTurns(messages),
      workspaceId,
      conversationId,
      thinking,
      parentRunId,
    })
    return { run: agentRunFromDTO(res.run) }
  },

  cancelAgentRun(id: number): Promise<void> {
    return apiClient.post<void>(`/api/ai/agent/runs/${id}/cancel`, {})
  },

  async streamAgentRunEvents(
    runId: number,
    onEvent: (event: StreamEvent) => void,
    afterSequence = 0,
    signal?: AbortSignal,
  ): Promise<void> {
    const res = await fetch(`/api/ai/agent/runs/${runId}/events?after=${afterSequence}`, { signal })

    if (!res.ok) {
      const err = await res.json()
      throw new Error(err.error || `Agent run events request failed: ${res.status}`)
    }

    await readSSEStream(res, onEvent)
  },

  async agentStream(
    model: string,
    messages: ChatMessage[],
    workspaceId: number,
    onEvent: (event: StreamEvent) => void,
    signal?: AbortSignal,
    thinking?: ThinkingConfig,
  ): Promise<void> {
    const res = await fetch('/api/ai/agent', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ model, turns: messagesToTurns(messages), workspaceId, thinking }),
      signal,
    })

    if (!res.ok) {
      const err = await res.json()
      throw new Error(err.error || `Agent request failed: ${res.status}`)
    }

    await readSSEStream(res, onEvent)
  },

  listConversations(workspaceId: number): Promise<{ conversations: Conversation[] }> {
    return apiClient.get<{ conversations: Conversation[] }>(`/api/ai/conversations?workspaceId=${workspaceId}`)
  },

  createConversation(workspaceId: number, model: string): Promise<{ conversation: Conversation }> {
    return apiClient.post<{ conversation: Conversation }>('/api/ai/conversations', { workspaceId, model })
  },

  deleteConversation(id: number): Promise<void> {
    return apiClient.delete(`/api/ai/conversations/${id}`)
  },

  async getMessages(conversationId: number): Promise<{ messages: ChatMessage[] }> {
    const res = await apiClient.get<{ turns: TurnDTO[] }>(`/api/ai/conversations/${conversationId}/messages`)
    return { messages: turnsToMessages(res.turns ?? []) }
  },

  saveMessages(conversationId: number, messages: ChatMessage[]): Promise<void> {
    return apiClient.put<void>(`/api/ai/conversations/${conversationId}/messages`, { turns: messagesToTurns(messages) })
  },
}

async function readSSEStream(
  res: Response,
  onEvent: (event: StreamEvent) => void,
): Promise<void> {
  const reader = res.body?.getReader()
  if (!reader) throw new Error('No response body')

  const decoder = new TextDecoder()
  let buffer = ''

  while (true) {
    const { done, value } = await reader.read()
    if (done) break

    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = lines.pop() || ''

    for (const line of lines) {
      if (!line.startsWith('data: ')) continue
      const data = line.slice(6).trim()
      if (!data) continue

      try {
        const event: StreamEvent = JSON.parse(data)
        onEvent(event)
      } catch {
        // skip malformed events
      }
    }
  }
}
