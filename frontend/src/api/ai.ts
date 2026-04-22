import { apiClient } from './client'

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

export interface PlanStep {
  title: string
  status: 'pending' | 'in_progress' | 'completed' | 'failed'
}

export interface StreamEvent {
  reasoningContent?: string
  thinkingState?: unknown
  content?: string
  toolCalls?: ToolCall[]
  toolResult?: ToolResult
  approvalRequired?: ApprovalRequest
  plan?: PlanStep[]
  done?: boolean
  error?: string
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
      body: JSON.stringify({ model, messages, thinking }),
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
      body: JSON.stringify({ model, messages, workspaceId, thinking }),
      signal,
    })

    if (!res.ok) {
      const err = await res.json()
      throw new Error(err.error || `Agent request failed: ${res.status}`)
    }

    await readSSEStream(res, onEvent)
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
