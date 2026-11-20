<script setup lang="ts">
import { nextTick, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import type { AgentRun } from '@/api/ai'
import { useAttentionStore } from '@/stores/attention'
import { useIsMobile } from '@/composables/useMediaQuery'
import InboxList from './InboxList.vue'

// The inbox, opened over whatever the user is doing from its button in the
// header or the IDE's title bar: a dropdown under the button, or a full-screen
// sheet on a phone. The caller draws the button in the trigger slot, so its
// own styles apply.
defineProps<{
  currentProjectId?: number | null
  currentWorkspaceId?: number | null
}>()

const emit = defineEmits<{ 'open-chat': [run: AgentRun] }>()

const PANEL_WIDTH = 420
const EDGE = 16
const GAP = 6

const attention = useAttentionStore()
const route = useRoute()
const isMobile = useIsMobile()
const anchor = ref<HTMLElement | null>(null)
const panel = ref<HTMLElement | null>(null)
const placement = ref({ top: 0, right: 0, maxHeight: 0 })

// The title bar clips what overflows it, so the panel sits on the body, under
// the button and inside the window.
function place() {
  const rect = anchor.value?.getBoundingClientRect()
  if (!rect) return
  const top = rect.bottom + GAP
  const right = Math.max(EDGE, Math.min(window.innerWidth - rect.right, window.innerWidth - EDGE - PANEL_WIDTH))
  placement.value = { top, right, maxHeight: window.innerHeight - top - EDGE }
}

function toggle() {
  attention.panelOpen = !attention.panelOpen
}

function close(restoreFocus = false) {
  attention.panelOpen = false
  if (restoreFocus) void nextTick(() => anchor.value?.querySelector<HTMLElement>('button, a')?.focus())
}

function openChat(run: AgentRun) {
  emit('open-chat', run)
}

function onDocumentPointer(e: PointerEvent) {
  const target = e.target as Node
  if (panel.value?.contains(target) || anchor.value?.contains(target)) return
  close()
}

function onKeydown(e: KeyboardEvent) {
  if (e.key !== 'Escape') return
  e.stopPropagation()
  close(true)
}

function listen(on: boolean) {
  if (on) {
    document.addEventListener('pointerdown', onDocumentPointer)
    document.addEventListener('keydown', onKeydown)
    window.addEventListener('resize', place)
    return
  }
  document.removeEventListener('pointerdown', onDocumentPointer)
  document.removeEventListener('keydown', onKeydown)
  window.removeEventListener('resize', place)
}

watch(
  () => attention.panelOpen,
  async (open) => {
    listen(open)
    if (!open) return
    place()
    await nextTick()
    panel.value?.focus()
  },
  { immediate: true },
)

// Back, forward, or any other way off the page closes it.
watch(() => route.fullPath, () => close())

onUnmounted(() => {
  listen(false)
  attention.panelOpen = false
})
</script>

<template>
  <span ref="anchor" class="inbox-anchor">
    <slot name="trigger" :open="attention.panelOpen" :toggle="toggle" />
  </span>
  <Teleport to="body">
    <Transition name="inbox-pop">
      <div
        v-if="attention.panelOpen"
        ref="panel"
        class="inbox-panel"
        :class="{ sheet: isMobile }"
        :style="isMobile ? undefined : {
          top: `${placement.top}px`,
          right: `${placement.right}px`,
          maxHeight: `min(70vh, ${placement.maxHeight}px)`,
        }"
        role="dialog"
        aria-label="Inbox"
        tabindex="-1"
      >
        <div class="inbox-header">
          <span class="inbox-title">
            Inbox
            <span v-if="attention.count > 0" class="inbox-count">{{ attention.count }} waiting</span>
          </span>
          <button class="inbox-close" aria-label="Close inbox" @click="close(true)">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M18 6 6 18" />
              <path d="m6 6 12 12" />
            </svg>
          </button>
        </div>
        <div class="inbox-body">
          <InboxList
            :current-project-id="currentProjectId"
            :current-workspace-id="currentWorkspaceId"
            @navigate="close()"
            @open-chat="openChat"
          />
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.inbox-anchor {
  display: inline-flex;
}

.inbox-panel {
  position: fixed;
  z-index: 1000;
  display: flex;
  flex-direction: column;
  width: 420px;
  max-width: calc(100vw - 2 * var(--space-4));
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-strong);
  outline: none;
}

.inbox-panel.sheet {
  inset: 0;
  width: auto;
  max-width: none;
  border: none;
  border-radius: 0;
  box-shadow: none;
  padding-bottom: env(safe-area-inset-bottom);
}

.inbox-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-2) var(--space-2) var(--space-4);
  border-bottom: 0.5px solid var(--border-default);
  flex-shrink: 0;
}

.inbox-title {
  display: inline-flex;
  align-items: baseline;
  gap: var(--space-2);
  font-size: 0.9rem;
  font-weight: 500;
}

.inbox-count {
  font-size: 0.75rem;
  font-weight: 400;
  color: var(--attention);
}

.inbox-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  transition: all var(--transition-fast);
}

.inbox-close:hover {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.inbox-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-3) var(--space-4) var(--space-4);
}

.sheet .inbox-header {
  min-height: 52px;
  padding-top: calc(var(--space-2) + env(safe-area-inset-top));
}

.sheet .inbox-close {
  width: 40px;
  height: 40px;
}

.inbox-pop-enter-active,
.inbox-pop-leave-active {
  transition: opacity var(--transition-fast), transform var(--transition-fast);
}

.inbox-pop-enter-from,
.inbox-pop-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
