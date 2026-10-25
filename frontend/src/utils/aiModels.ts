import type { AgentDefaults, AIModel } from '@/api/ai'

export const DEFAULT_AI_MODEL_ID = 'mistral-medium-3-5'

// Built-in agent IDs, as in internal/ai/domain.
export const CODING_AGENT_ID = 'default'
export const REVIEW_AGENT_ID = 'review'

export function isBuiltInAgentId(agentId: string): boolean {
  return agentId === CODING_AGENT_ID || agentId === REVIEW_AGENT_ID
}

// The model to start on: the agent's admin-set default when it can be used,
// otherwise devpad's own.
export function defaultAIModel(availableModels: AIModel[], agentDefaults?: AgentDefaults): AIModel | undefined {
  return (
    availableModels.find((model) => model.id === agentDefaults?.model) ??
    availableModels.find((model) => model.id === DEFAULT_AI_MODEL_ID) ??
    availableModels[0]
  )
}

// The effort to start on with a model: the agent's admin-set default when it
// is for this model, otherwise the model's own.
export function defaultThinkingEffort(model: AIModel, agentDefaults?: AgentDefaults): string {
  const effort = agentDefaults?.model === model.id ? agentDefaults.effort : undefined
  if (effort && model.thinking.supportedEfforts?.includes(effort)) return effort
  return model.thinking.defaultEffort ?? model.thinking.supportedEfforts?.[0] ?? ''
}

export function thinkingEffortLabel(effort: string): string {
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
