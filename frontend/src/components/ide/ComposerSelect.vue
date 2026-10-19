<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { useIsMobile } from '@/composables/useMediaQuery'

export interface ComposerSelectOption {
  value: string
  label: string
  description?: string
}

const props = defineProps<{
  modelValue: string
  options: ComposerSelectOption[]
  title: string
  prefix?: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

// A compact picker for the chat composer. Native selects get forced to 16px on
// touch devices (to stop iOS focus zoom) and open a generic OS picker, so this
// renders a small chip that opens a popover on desktop and a bottom sheet on phones.
const isMobile = useIsMobile()
const open = ref(false)
const triggerEl = ref<HTMLButtonElement | null>(null)
const menuEl = ref<HTMLElement | null>(null)
const popoverStyle = ref<Record<string, string>>({})

const selected = computed(() => props.options.find((option) => option.value === props.modelValue) ?? null)

const POPOVER_GAP = 6
const VIEWPORT_MARGIN = 8

function positionPopover() {
  const rect = triggerEl.value?.getBoundingClientRect()
  if (!rect) return
  popoverStyle.value = {
    left: `${Math.max(VIEWPORT_MARGIN, rect.left)}px`,
    bottom: `${window.innerHeight - rect.top + POPOVER_GAP}px`,
    minWidth: `${Math.max(rect.width, 180)}px`,
    maxHeight: `${Math.max(160, rect.top - POPOVER_GAP - VIEWPORT_MARGIN)}px`,
  }
}

// The popover opens at the trigger's left edge; shift it back on-screen when the
// composer sits near the right edge (e.g. the AI side panel).
function clampPopover() {
  const menu = menuEl.value
  if (!menu) return
  const overflow = menu.getBoundingClientRect().right - (window.innerWidth - VIEWPORT_MARGIN)
  if (overflow > 0) {
    const left = parseFloat(popoverStyle.value.left ?? '0')
    popoverStyle.value = { ...popoverStyle.value, left: `${Math.max(VIEWPORT_MARGIN, left - overflow)}px` }
  }
}

function optionButtons(): HTMLButtonElement[] {
  return Array.from(menuEl.value?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? [])
}

function focusOption(index: number) {
  const buttons = optionButtons()
  if (buttons.length === 0) return
  const clamped = (index + buttons.length) % buttons.length
  buttons[clamped]?.focus()
}

async function openMenu() {
  if (open.value || props.options.length === 0) return
  if (!isMobile.value) positionPopover()
  open.value = true
  await nextTick()
  if (!isMobile.value) clampPopover()
  const selectedIndex = props.options.findIndex((option) => option.value === props.modelValue)
  focusOption(Math.max(0, selectedIndex))
}

function close(restoreFocus = true) {
  if (!open.value) return
  open.value = false
  if (restoreFocus) triggerEl.value?.focus({ preventScroll: true })
}

function toggle() {
  if (open.value) close()
  else void openMenu()
}

function select(value: string) {
  if (value !== props.modelValue) emit('update:modelValue', value)
  close()
}

function onTriggerKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    void openMenu()
  }
}

function onMenuKeydown(event: KeyboardEvent) {
  const buttons = optionButtons()
  const current = buttons.indexOf(document.activeElement as HTMLButtonElement)
  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault()
      focusOption(current + 1)
      break
    case 'ArrowUp':
      event.preventDefault()
      focusOption(current - 1)
      break
    case 'Home':
      event.preventDefault()
      focusOption(0)
      break
    case 'End':
      event.preventDefault()
      focusOption(buttons.length - 1)
      break
    case 'Escape':
    case 'Tab':
      event.preventDefault()
      close()
      break
  }
}

// On phones the window resizes as the keyboard hides, which is expected right
// after tapping the chip; only desktop popovers need to follow the trigger.
function onWindowResize() {
  if (!isMobile.value) {
    positionPopover()
    void nextTick(clampPopover)
  }
}

watch(open, (isOpen) => {
  if (isOpen) window.addEventListener('resize', onWindowResize)
  else window.removeEventListener('resize', onWindowResize)
})

onUnmounted(() => {
  window.removeEventListener('resize', onWindowResize)
})
</script>

<template>
  <span class="composer-select-root">
    <button
      ref="triggerEl"
      type="button"
      class="composer-select"
      :class="{ open }"
      :title="title"
      :aria-label="`${title}: ${selected?.label ?? 'None'}`"
      aria-haspopup="listbox"
      :aria-expanded="open"
      @click="toggle"
      @keydown="onTriggerKeydown"
    >
      <span v-if="prefix" class="composer-select-prefix">{{ prefix }}</span>
      <span class="composer-select-label">{{ selected?.label ?? 'None' }}</span>
      <svg class="composer-select-chevron" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="m6 9 6 6 6-6" />
      </svg>
    </button>

    <Teleport to="body">
      <Transition :name="isMobile ? 'composer-sheet' : 'composer-popover'">
        <div
          v-if="open"
          class="composer-select-layer"
          :class="{ 'is-sheet': isMobile }"
          @click.self="close()"
        >
          <div
            ref="menuEl"
            class="composer-select-menu"
            :style="isMobile ? undefined : popoverStyle"
            @keydown="onMenuKeydown"
          >
            <div v-if="isMobile" class="composer-sheet-header">
              <span class="composer-sheet-grabber" aria-hidden="true" />
              <span class="composer-sheet-title">{{ title }}</span>
            </div>
            <div class="composer-select-list" role="listbox" :aria-label="title">
              <button
                v-for="option in options"
                :key="option.value"
                type="button"
                role="option"
                class="composer-select-option"
                :class="{ selected: option.value === modelValue }"
                :aria-selected="option.value === modelValue"
                @click="select(option.value)"
              >
                <span class="composer-select-option-text">
                  <span class="composer-select-option-label">{{ option.label }}</span>
                  <span v-if="option.description" class="composer-select-option-description">
                    {{ option.description }}
                  </span>
                </span>
                <svg
                  v-if="option.value === modelValue"
                  class="composer-select-check"
                  width="14"
                  height="14"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2.5"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  aria-hidden="true"
                >
                  <path d="M20 6 9 17l-5-5" />
                </svg>
              </button>
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>
  </span>
</template>

<style scoped>
.composer-select-root {
  display: inline-flex;
  min-width: 0;
}

.composer-select {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  max-width: 100%;
  min-height: 24px;
  padding: 2px 6px 2px 8px;
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--bg-raised) 70%, var(--bg-elevated));
  border: 0.5px solid var(--border-default);
  color: var(--text-secondary);
  font-size: 0.75rem;
  line-height: 1.3;
  transition: border-color var(--transition-fast), color var(--transition-fast), background var(--transition-fast);
}

.composer-select:hover,
.composer-select.open {
  border-color: var(--border-strong);
  color: var(--text-primary);
}

.composer-select-prefix {
  flex-shrink: 0;
  color: var(--text-tertiary);
}

.composer-select-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.composer-select-chevron {
  flex-shrink: 0;
  color: var(--text-tertiary);
  transition: transform var(--transition-fast);
}

.composer-select.open .composer-select-chevron {
  transform: rotate(180deg);
}

/* Desktop popover */
.composer-select-layer {
  position: fixed;
  inset: 0;
  z-index: 1100;
}

.composer-select-menu {
  position: fixed;
  display: flex;
  flex-direction: column;
  max-width: min(320px, calc(100vw - 16px));
  overflow: hidden;
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-popover);
}

.composer-select-list {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: var(--space-1);
  overflow-y: auto;
  overscroll-behavior: contain;
}

.composer-select-option {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  padding: 6px var(--space-2);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  text-align: left;
  font-size: 0.78rem;
  transition: background var(--transition-fast), color var(--transition-fast);
}

.composer-select-option:hover,
.composer-select-option:focus-visible {
  background: var(--bg-hover);
  color: var(--text-primary);
  box-shadow: none;
}

.composer-select-option.selected {
  color: var(--text-primary);
}

.composer-select-option-text {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}

.composer-select-option-label,
.composer-select-option-description {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.composer-select-option-description {
  font-size: 0.68rem;
  color: var(--text-tertiary);
}

.composer-select-check {
  flex-shrink: 0;
  color: var(--accent);
}

/* Phone bottom sheet */
.composer-select-layer.is-sheet {
  display: flex;
  align-items: flex-end;
  background: var(--bg-overlay);
}

.is-sheet .composer-select-menu {
  position: relative;
  width: 100%;
  max-width: none;
  max-height: 70dvh;
  padding: 0 env(safe-area-inset-right) env(safe-area-inset-bottom) env(safe-area-inset-left);
  border: none;
  border-top: 0.5px solid var(--border-strong);
  border-radius: 16px 16px 0 0;
  box-shadow: var(--shadow-strong);
}

.composer-sheet-header {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4) var(--space-2);
  flex-shrink: 0;
}

.composer-sheet-grabber {
  width: 36px;
  height: 4px;
  border-radius: 2px;
  background: var(--border-strong);
}

.composer-sheet-title {
  align-self: flex-start;
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-tertiary);
}

.is-sheet .composer-select-list {
  gap: 2px;
  padding: 0 var(--space-2) var(--space-3);
}

.is-sheet .composer-select-option {
  min-height: 48px;
  padding: 10px var(--space-3);
  border-radius: var(--radius-lg);
  font-size: 1.05rem;
}

.is-sheet .composer-select-option:hover {
  background: none;
}

.is-sheet .composer-select-option.selected {
  background: var(--bg-selected);
}

.is-sheet .composer-select-option-description {
  font-size: 0.85rem;
}

.is-sheet .composer-select-check {
  width: 18px;
  height: 18px;
}

/* Transitions — the layer's own transition sets the duration Vue waits for. */
.composer-popover-enter-active,
.composer-popover-leave-active {
  transition: opacity 120ms ease;
}

.composer-popover-enter-active .composer-select-menu,
.composer-popover-leave-active .composer-select-menu {
  transition: transform 120ms ease;
}

.composer-popover-enter-from,
.composer-popover-leave-to {
  opacity: 0;
}

.composer-popover-enter-from .composer-select-menu,
.composer-popover-leave-to .composer-select-menu {
  transform: translateY(4px);
}

.composer-sheet-enter-active,
.composer-sheet-leave-active {
  transition: opacity 220ms ease;
}

.composer-sheet-enter-active .composer-select-menu,
.composer-sheet-leave-active .composer-select-menu {
  transition: transform 220ms cubic-bezier(0.32, 0.72, 0, 1);
}

.composer-sheet-enter-from,
.composer-sheet-leave-to {
  opacity: 0;
}

.composer-sheet-enter-from .composer-select-menu,
.composer-sheet-leave-to .composer-select-menu {
  transform: translateY(100%);
}

/* Matches MOBILE_MEDIA_QUERY in useMediaQuery.ts: bigger chips for touch. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .composer-select {
    min-height: 30px;
    padding: 4px 8px 4px 10px;
    font-size: 0.86rem;
    border-radius: var(--radius-lg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .composer-sheet-enter-active,
  .composer-sheet-leave-active,
  .composer-sheet-enter-active .composer-select-menu,
  .composer-sheet-leave-active .composer-select-menu {
    transition-duration: 0ms;
  }
}
</style>
