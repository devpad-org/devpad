<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { apiClient } from '@/api/client'

const status = ref<string>('checking...')

onMounted(async () => {
  try {
    const data = await apiClient.get<{ status: string }>('/api/health')
    status.value = data.status
  } catch {
    status.value = 'unreachable'
  }
})
</script>

<template>
  <div class="home">
    <div class="hero">
      <h1 class="title">Devpad</h1>
      <p class="subtitle">Cloud development workspaces</p>
      <div class="status-badge" :class="status">
        <span class="dot" />
        API: {{ status }}
      </div>
    </div>
  </div>
</template>

<style scoped>
.home {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
}

.hero {
  text-align: center;
}

.title {
  font-size: 3rem;
  font-weight: 700;
  background: linear-gradient(135deg, var(--accent-blue), var(--accent-purple));
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  margin-bottom: var(--space-2);
}

.subtitle {
  color: var(--text-secondary);
  font-size: 1.1rem;
  margin-bottom: var(--space-6);
}

.status-badge {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  border: 1px solid var(--border-default);
  font-family: var(--font-mono);
  font-size: 0.85rem;
  color: var(--text-secondary);
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-muted);
}

.status-badge.ok .dot {
  background: var(--accent-green);
  box-shadow: 0 0 6px var(--accent-green);
}

.status-badge.unreachable .dot {
  background: var(--accent-rose);
  box-shadow: 0 0 6px var(--accent-rose);
}
</style>
