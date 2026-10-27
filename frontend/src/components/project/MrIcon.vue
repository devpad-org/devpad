<script setup lang="ts">
// Line icons for the project, task and merge request views, drawn on a 24px grid.
export type MrIconName =
  | 'open'
  | 'merged'
  | 'closed'
  | 'branch'
  | 'commit'
  | 'arrow-right'
  | 'chevron-left'
  | 'chevron-right'
  | 'chevron-down'
  | 'check'
  | 'check-circle'
  | 'comment'
  | 'alert'
  | 'warning'
  | 'user'
  | 'bot'
  | 'task'
  | 'file'
  | 'folder'
  | 'search'
  | 'close'
  | 'split'
  | 'unified'
  | 'terminal'
  | 'clock'
  | 'circle'
  | 'ban'
  | 'eye'
  | 'pencil'
  | 'trash'
  | 'play'
  | 'stop'
  | 'send'
  | 'retry'

withDefaults(defineProps<{ name: MrIconName; size?: number; strokeWidth?: number }>(), {
  size: 14,
  strokeWidth: 2,
})
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    :stroke-width="strokeWidth"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    class="mr-icon"
  >
    <template v-if="name === 'open'">
      <circle cx="18" cy="18" r="3" /><circle cx="6" cy="6" r="3" /><path d="M13 6h3a2 2 0 0 1 2 2v7" /><path d="M6 9v12" />
    </template>
    <template v-else-if="name === 'merged'">
      <circle cx="18" cy="18" r="3" /><circle cx="6" cy="6" r="3" /><path d="M6 21V9a9 9 0 0 0 9 9" />
    </template>
    <template v-else-if="name === 'closed'">
      <circle cx="6" cy="6" r="3" /><path d="M6 9v12" /><path d="m21 3-6 6" /><path d="m21 9-6-6" /><path d="M18 11.5V15" /><circle cx="18" cy="18" r="3" />
    </template>
    <template v-else-if="name === 'branch'">
      <path d="M6 3v12" /><circle cx="18" cy="6" r="3" /><circle cx="6" cy="18" r="3" /><path d="M18 9a9 9 0 0 1-9 9" />
    </template>
    <template v-else-if="name === 'commit'">
      <circle cx="12" cy="12" r="3" /><path d="M3 12h6" /><path d="M15 12h6" />
    </template>
    <template v-else-if="name === 'arrow-right'">
      <path d="M5 12h14" /><path d="m12 5 7 7-7 7" />
    </template>
    <template v-else-if="name === 'chevron-left'">
      <path d="m15 18-6-6 6-6" />
    </template>
    <template v-else-if="name === 'chevron-right'">
      <path d="m9 18 6-6-6-6" />
    </template>
    <template v-else-if="name === 'chevron-down'">
      <path d="m6 9 6 6 6-6" />
    </template>
    <template v-else-if="name === 'check'">
      <path d="M20 6 9 17l-5-5" />
    </template>
    <template v-else-if="name === 'check-circle'">
      <circle cx="12" cy="12" r="9" />
      <path d="m8.5 12 2.5 2.5 4.5-5" />
    </template>
    <template v-else-if="name === 'comment'">
      <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
    </template>
    <template v-else-if="name === 'alert'">
      <circle cx="12" cy="12" r="10" /><path d="M12 8v4" /><path d="M12 16h.01" />
    </template>
    <template v-else-if="name === 'warning'">
      <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3" /><path d="M12 9v4" /><path d="M12 17h.01" />
    </template>
    <template v-else-if="name === 'user'">
      <path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2" /><circle cx="12" cy="7" r="4" />
    </template>
    <template v-else-if="name === 'bot'">
      <path d="M12 8V4H8" /><rect width="16" height="12" x="4" y="8" rx="2" /><path d="M2 14h2" /><path d="M20 14h2" /><path d="M15 13v2" /><path d="M9 13v2" />
    </template>
    <template v-else-if="name === 'task'">
      <rect width="8" height="4" x="8" y="2" rx="1" /><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2" /><path d="m9 14 2 2 4-4" />
    </template>
    <template v-else-if="name === 'file'">
      <path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z" /><path d="M14 2v4a2 2 0 0 0 2 2h4" />
    </template>
    <template v-else-if="name === 'folder'">
      <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
    </template>
    <template v-else-if="name === 'search'">
      <circle cx="11" cy="11" r="8" /><path d="m21 21-4.3-4.3" />
    </template>
    <template v-else-if="name === 'close'">
      <path d="M18 6 6 18" /><path d="m6 6 12 12" />
    </template>
    <template v-else-if="name === 'split'">
      <rect width="18" height="18" x="3" y="3" rx="2" /><path d="M12 3v18" />
    </template>
    <template v-else-if="name === 'unified'">
      <rect width="18" height="18" x="3" y="3" rx="2" /><path d="M3 12h18" />
    </template>
    <template v-else-if="name === 'terminal'">
      <path d="m4 17 6-6-6-6" /><path d="M12 19h8" />
    </template>
    <template v-else-if="name === 'clock'">
      <circle cx="12" cy="12" r="10" /><path d="M12 6v6l4 2" />
    </template>
    <template v-else-if="name === 'circle'">
      <circle cx="12" cy="12" r="9" />
    </template>
    <template v-else-if="name === 'ban'">
      <circle cx="12" cy="12" r="10" /><path d="m4.9 4.9 14.2 14.2" />
    </template>
    <template v-else-if="name === 'eye'">
      <path d="M2.06 12.35a1 1 0 0 1 0-.7 10.75 10.75 0 0 1 19.88 0 1 1 0 0 1 0 .7 10.75 10.75 0 0 1-19.88 0" /><circle cx="12" cy="12" r="3" />
    </template>
    <template v-else-if="name === 'pencil'">
      <path d="M21.17 6.81a1 1 0 0 0-3.98-3.98L3.84 16.17a2 2 0 0 0-.5.83l-1.32 4.35a.5.5 0 0 0 .62.62l4.35-1.32a2 2 0 0 0 .83-.5z" /><path d="m15 5 4 4" />
    </template>
    <template v-else-if="name === 'trash'">
      <path d="M3 6h18" /><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6" /><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" />
    </template>
    <template v-else-if="name === 'play'">
      <path d="M6 3l14 9-14 9z" />
    </template>
    <template v-else-if="name === 'stop'">
      <circle cx="12" cy="12" r="10" /><rect x="9" y="9" width="6" height="6" rx="1" />
    </template>
    <template v-else-if="name === 'send'">
      <path d="M14.54 21.69a.5.5 0 0 0 .94-.03l6.5-19a.5.5 0 0 0-.64-.64l-19 6.5a.5.5 0 0 0-.02.94l7.93 3.18a2 2 0 0 1 1.11 1.11z" /><path d="m21.85 2.15-10.94 10.94" />
    </template>
    <template v-else-if="name === 'retry'">
      <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" /><path d="M3 3v5h5" />
    </template>
  </svg>
</template>

<style scoped>
.mr-icon {
  flex-shrink: 0;
}
</style>
