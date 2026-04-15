<script setup lang="ts">
import { ref, nextTick } from 'vue'

interface Message {
  role: 'user' | 'assistant'
  content: string
}

const messages = ref<Message[]>([
  {
    role: 'assistant',
    content: 'Hello! I\'m your AI coding assistant. I can help you write code, debug issues, explain concepts, and more. What would you like to work on?',
  },
])

const inputValue = ref('')
const chatBody = ref<HTMLElement | null>(null)

const placeholderResponses = [
  'I\'d be happy to help with that! This is a placeholder response — the AI agent isn\'t connected yet, but once it is I\'ll be able to assist with your code.',
  'Great question! When the agent is fully connected, I\'ll be able to read your files, suggest changes, and run commands. For now, this is a preview of the interface.',
  'I can see you\'re working on something interesting. Once connected to the workspace, I\'ll have full context of your project and can provide targeted help.',
  'That\'s a common pattern in modern web development. I\'ll be able to provide detailed explanations and code examples once the agent backend is wired up.',
]

let responseIndex = 0

async function sendMessage() {
  const text = inputValue.value.trim()
  if (!text) return

  messages.value.push({ role: 'user', content: text })
  inputValue.value = ''

  await nextTick()
  scrollToBottom()

  // Simulate a brief delay
  setTimeout(async () => {
    messages.value.push({
      role: 'assistant',
      content: placeholderResponses[responseIndex % placeholderResponses.length],
    })
    responseIndex++
    await nextTick()
    scrollToBottom()
  }, 600)
}

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
      <span class="agent-badge">Preview</span>
    </div>

    <div ref="chatBody" class="agent-body">
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
        <div class="msg-content">{{ msg.content }}</div>
      </div>
    </div>

    <div class="agent-input-area">
      <textarea
        v-model="inputValue"
        class="agent-input"
        placeholder="Ask the AI agent…"
        rows="2"
        @keydown.enter.exact.prevent="sendMessage"
      />
      <button class="agent-send" @click="sendMessage" title="Send">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="m22 2-7 20-4-9-9-4Z" />
          <path d="M22 2 11 13" />
        </svg>
      </button>
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
  display: flex;
  align-items: flex-end;
  gap: var(--space-2);
  padding: var(--space-3);
  border-top: 1px solid var(--border-default);
  flex-shrink: 0;
}

.agent-input {
  flex: 1;
  background: var(--bg-surface-alt);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-3);
  font-family: var(--font-sans);
  font-size: 0.8rem;
  color: var(--text-primary);
  resize: none;
  outline: none;
  transition: border-color var(--transition-fast);
  line-height: 1.5;
}

.agent-input::placeholder {
  color: var(--text-muted);
}

.agent-input:focus {
  border-color: var(--accent-purple);
}

.agent-send {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: var(--radius-md);
  background: linear-gradient(135deg, var(--accent-purple), var(--accent-blue));
  color: white;
  flex-shrink: 0;
  transition: opacity var(--transition-fast);
}

.agent-send:hover {
  opacity: 0.85;
}
</style>
