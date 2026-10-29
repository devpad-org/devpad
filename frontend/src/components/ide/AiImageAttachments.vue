<script setup lang="ts">
import { ref } from 'vue'

export interface ImageAttachmentItem {
  key: string
  // The stored attachment, or a local preview while it uploads.
  src: string
  // Opens the full image. Absent until it is stored.
  href?: string
  status?: 'uploading' | 'error'
  error?: string
}

defineProps<{
  items: ImageAttachmentItem[]
  removable?: boolean
}>()

const emit = defineEmits<{
  (e: 'remove', key: string): void
}>()

// Images whose attachment is gone, such as after their conversation was deleted.
const broken = ref<Record<string, boolean>>({})
</script>

<template>
  <div class="image-attachments">
    <div
      v-for="item in items"
      :key="item.key"
      class="image-attachment"
      :class="{ 'image-attachment--error': item.status === 'error', 'image-attachment--uploading': item.status === 'uploading' }"
      :title="item.error"
    >
      <span v-if="broken[item.key]" class="image-attachment-missing">Image unavailable</span>
      <a v-else-if="item.href" :href="item.href" target="_blank" rel="noopener" title="Open the full image">
        <img :src="item.src" alt="Attached image" @error="broken[item.key] = true" />
      </a>
      <img v-else :src="item.src" alt="Attached image" @error="broken[item.key] = true" />
      <span v-if="item.status === 'uploading'" class="image-attachment-spinner" aria-label="Uploading" />
      <span v-else-if="item.status === 'error'" class="image-attachment-error">Failed</span>
      <button
        v-if="removable"
        type="button"
        class="image-attachment-remove"
        title="Remove image"
        aria-label="Remove image"
        @click.stop="emit('remove', item.key)"
      >
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round">
          <path d="M18 6 6 18" />
          <path d="m6 6 12 12" />
        </svg>
      </button>
    </div>
  </div>
</template>

<style scoped>
.image-attachments {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.image-attachment {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  height: 64px;
  min-width: 48px;
  max-width: 160px;
  overflow: hidden;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-base);
}

.image-attachment a {
  display: flex;
  height: 100%;
}

.image-attachment img {
  display: block;
  height: 100%;
  max-width: 160px;
  object-fit: cover;
}

.image-attachment--uploading img {
  opacity: 0.5;
}

.image-attachment--error {
  border-color: var(--error-border);
}

.image-attachment--error img {
  opacity: 0.35;
}

.image-attachment-missing {
  padding: 0 var(--space-2);
  color: var(--text-muted);
  font-size: 0.68rem;
  text-align: center;
}

.image-attachment-spinner {
  position: absolute;
  width: 16px;
  height: 16px;
  border: 2px solid var(--border-strong);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: image-attachment-spin 0.8s linear infinite;
}

.image-attachment-error {
  position: absolute;
  padding: 1px 6px;
  border-radius: var(--radius-sm);
  background: var(--error-bg);
  color: var(--accent-rose);
  font-size: 0.66rem;
  font-weight: 600;
}

.image-attachment-remove {
  position: absolute;
  top: 3px;
  right: 3px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border: 0.5px solid var(--border-strong);
  border-radius: 50%;
  background: var(--bg-elevated);
  color: var(--text-secondary);
  opacity: 0.9;
  transition: color var(--transition-fast), background var(--transition-fast);
}

.image-attachment-remove:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

@keyframes image-attachment-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
