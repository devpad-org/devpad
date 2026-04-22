import { ref } from 'vue'
import { defineStore } from 'pinia'
import { aiApi, type Conversation } from '@/api/ai'

export { type Conversation }

export const useConversationStore = defineStore('chatHistory', () => {
  const conversations = ref<Conversation[]>([])
  const activeConversationId = ref<number | null>(null)

  async function fetchConversations(workspaceId: number): Promise<void> {
    try {
      const res = await aiApi.listConversations(workspaceId)
      conversations.value = res.conversations
    } catch {
      // silently ignore — list will remain stale
    }
  }

  async function createConversation(workspaceId: number, model: string): Promise<Conversation> {
    const res = await aiApi.createConversation(workspaceId, model)
    conversations.value = [res.conversation, ...conversations.value]
    return res.conversation
  }

  async function deleteConversation(id: number): Promise<void> {
    // Optimistic removal
    conversations.value = conversations.value.filter((c) => c.id !== id)
    try {
      await aiApi.deleteConversation(id)
    } catch {
      // Deletion failed — the item is already removed from the list.
      // The list will resync on the next fetchConversations call.
    }
  }

  function setActive(id: number | null): void {
    activeConversationId.value = id
  }

  return {
    conversations,
    activeConversationId,
    fetchConversations,
    createConversation,
    deleteConversation,
    setActive,
  }
})
