import type { AIModel } from '@/api/ai'

export const DEFAULT_AI_MODEL_ID = 'mistral-medium-3-5'

export function defaultAIModel(availableModels: AIModel[]): AIModel | undefined {
  return availableModels.find((model) => model.id === DEFAULT_AI_MODEL_ID) ?? availableModels[0]
}

export function defaultThinkingEffort(model: AIModel): string {
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
