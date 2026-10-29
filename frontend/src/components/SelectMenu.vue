<script setup lang="ts" generic="T extends string | number">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { useIsMobile } from '@/composables/useMediaQuery'

export interface SelectMenuOption<V extends string | number = string> {
  value: V
  label: string
  description?: string
}

const props = withDefaults(defineProps<{
  modelValue: T
  options: SelectMenuOption<T>[]
  title: string
  prefix?: string
  placeholder?: string
  // 'chip' is a compact toolbar control; 'field' fills its row like a form input.
  variant?: 'chip' | 'field'
  // The side the popover opens on when there's room; it flips when there isn't.
  placement?: 'above' | 'below'
  mono?: boolean
  disabled?: boolean
  id?: string
}>(), {
  prefix: undefined,
  placeholder: 'None',
  variant: 'chip',
  placement: 'below',
  id: undefined,
})

const emit = defineEmits<{
  'update:modelValue': [value: T]
}>()

// A select that stays usable on phones. Native selects get forced to 16px on
// touch devices (to stop iOS focus zoom) and open a generic OS picker, so this
// renders a trigger that opens a popover on desktop and a bottom sheet on phones.
const isMobile = useIsMobile()
const open = ref(false)
const triggerEl = ref<HTMLButtonElement | null>(null)
const menuEl = ref<HTMLElement | null>(null)
const popoverStyle = ref<Record<string, string>>({})

const selected = computed(() => props.options.find((option) => option.value === props.modelValue) ?? null)

const POPOVER_GAP = 6
const VIEWPORT_MARGIN = 8
const MIN_POPOVER_HEIGHT = 160

function positionPopover() {
  const rect = triggerEl.value?.getBoundingClientRect()
  if (!rect) return
  const roomAbove = rect.top - POPOVER_GAP - VIEWPORT_MARGIN
  const roomBelow = window.innerHeight - rect.bottom - POPOVER_GAP - VIEWPORT_MARGIN
  const [preferred, other] = props.placement === 'above' ? [roomAbove, roomBelow] : [roomBelow, roomAbove]
  const flip = preferred < MIN_POPOVER_HEIGHT && other > preferred
  const above = (props.placement === 'above') !== flip
  popoverStyle.value = {
    left: `${Math.max(VIEWPORT_MARGIN, rect.left)}px`,
    ...(above
      ? { bottom: `${window.innerHeight - rect.top + POPOVER_GAP}px` }
      : { top: `${rect.bottom + POPOVER_GAP}px` }),
    minWidth: `${Math.max(rect.width, 180)}px`,
    maxHeight: `${Math.max(MIN_POPOVER_HEIGHT, above ? roomAbove : roomBelow)}px`,
  }
}

// The popover opens at the trigger's left edge; shift it back on-screen when the
// trigger sits near the right edge (e.g. the AI side panel).
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
  if (open.value || props.disabled || props.options.length === 0) return
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

function select(value: T) {
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
      // A select in a modal shouldn't let Escape close the modal too.
      event.stopPropagation()
      close()
      break
  }
}

// On phones the window resizes as the keyboard hides, which is expected right
// after tapping the trigger; only desktop popovers need to follow the trigger.
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
  <span class="select-menu-root" :class="[`is-${variant}`, { mono }]">
    <button
      :id="id"
      ref="triggerEl"
      type="button"
      class="select-menu"
      :class="{ open }"
      :title="title"
      :disabled="disabled"
      :aria-label="`${title}: ${selected?.label ?? placeholder}`"
      aria-haspopup="listbox"
      :aria-expanded="open"
      @click="toggle"
      @keydown="onTriggerKeydown"
    >
      <span v-if="prefix" class="select-menu-prefix">{{ prefix }}</span>
      <span class="select-menu-label" :class="{ empty: !selected }">{{ selected?.label ?? placeholder }}</span>
      <svg class="select-menu-chevron" :width="variant === 'field' ? 12 : 10" :height="variant === 'field' ? 12 : 10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="m6 9 6 6 6-6" />
      </svg>
    </button>

    <Teleport to="body">
      <Transition :name="isMobile ? 'select-sheet' : 'select-popover'">
        <div
          v-if="open"
          class="select-menu-layer"
          :class="{ 'is-sheet': isMobile, mono }"
          @click.self="close()"
        >
          <div
            ref="menuEl"
            class="select-menu-menu"
            :style="isMobile ? undefined : popoverStyle"
            @keydown="onMenuKeydown"
          >
            <div v-if="isMobile" class="select-sheet-header">
              <span class="select-sheet-grabber" aria-hidden="true" />
              <span class="select-sheet-title">{{ title }}</span>
            </div>
            <div class="select-menu-list" role="listbox" :aria-label="title">
              <button
                v-for="option in options"
                :key="option.value"
                type="button"
                role="option"
                class="select-menu-option"
                :class="{ selected: option.value === modelValue }"
                :aria-selected="option.value === modelValue"
                @click="select(option.value)"
              >
                <span class="select-menu-option-text">
                  <span class="select-menu-option-label">{{ option.label }}</span>
                  <span v-if="option.description" class="select-menu-option-description">
                    {{ option.description }}
                  </span>
                </span>
                <svg
                  v-if="option.value === modelValue"
                  class="select-menu-check"
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
/* :where() keeps the root's sizing below any class a parent puts on it. */
:where(.select-menu-root) {
  display: inline-flex;
  min-width: 0;
}

.select-menu {
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

.select-menu:hover:not(:disabled),
.select-menu.open {
  border-color: var(--border-strong);
  color: var(--text-primary);
}

.select-menu:disabled {
  color: var(--text-muted);
  cursor: default;
}

.select-menu-prefix {
  flex-shrink: 0;
  color: var(--text-tertiary);
}

.select-menu-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.select-menu-label.empty {
  color: var(--text-muted);
}

.select-menu-chevron {
  flex-shrink: 0;
  color: var(--text-tertiary);
  transition: transform var(--transition-fast);
}

.select-menu.open .select-menu-chevron {
  transform: rotate(180deg);
}

/* Field: sized and colored like .form-input, filling its row. */
:where(.select-menu-root.is-field) {
  display: flex;
  width: 100%;
}

.is-field .select-menu {
  flex: 1;
  gap: var(--space-2);
  min-height: 35px;
  padding: var(--space-2) var(--space-2) var(--space-2) var(--space-3);
  background: var(--bg-primary);
  color: var(--text-primary);
  font-size: 0.85rem;
  text-align: left;
}

.is-field .select-menu-label {
  flex: 1;
}

.is-field .select-menu:focus-visible,
.is-field .select-menu.open {
  outline: none;
  border-color: var(--accent-blue);
  box-shadow: none;
}

.is-field .select-menu:disabled {
  color: var(--text-muted);
}

.mono .select-menu-label,
.mono .select-menu-option-label {
  font-family: var(--font-mono);
}

.is-field.mono .select-menu {
  font-size: 0.8rem;
}

/* Desktop popover */
.select-menu-layer {
  position: fixed;
  inset: 0;
  z-index: 1100;
}

.select-menu-menu {
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

.select-menu-list {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: var(--space-1);
  overflow-y: auto;
  overscroll-behavior: contain;
}

.select-menu-option {
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

.select-menu-option:hover,
.select-menu-option:focus-visible {
  background: var(--bg-hover);
  color: var(--text-primary);
  box-shadow: none;
}

.select-menu-option.selected {
  color: var(--text-primary);
}

.select-menu-option-text {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}

.select-menu-option-label,
.select-menu-option-description {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.select-menu-option-description {
  font-size: 0.68rem;
  color: var(--text-tertiary);
}

.select-menu-check {
  flex-shrink: 0;
  color: var(--accent);
}

/* Phone bottom sheet */
.select-menu-layer.is-sheet {
  display: flex;
  align-items: flex-end;
  background: var(--bg-overlay);
}

.is-sheet .select-menu-menu {
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

.select-sheet-header {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4) var(--space-2);
  flex-shrink: 0;
}

.select-sheet-grabber {
  width: 36px;
  height: 4px;
  border-radius: 2px;
  background: var(--border-strong);
}

.select-sheet-title {
  align-self: flex-start;
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-tertiary);
}

.is-sheet .select-menu-list {
  gap: 2px;
  padding: 0 var(--space-2) var(--space-3);
}

.is-sheet .select-menu-option {
  min-height: 48px;
  padding: 10px var(--space-3);
  border-radius: var(--radius-lg);
  font-size: 1.05rem;
}

.is-sheet .select-menu-option:hover {
  background: none;
}

.is-sheet .select-menu-option.selected {
  background: var(--bg-selected);
}

.is-sheet .select-menu-option-description {
  font-size: 0.85rem;
}

.is-sheet .select-menu-check {
  width: 18px;
  height: 18px;
}

/* Transitions — the layer's own transition sets the duration Vue waits for. */
.select-popover-enter-active,
.select-popover-leave-active {
  transition: opacity 120ms ease;
}

.select-popover-enter-active .select-menu-menu,
.select-popover-leave-active .select-menu-menu {
  transition: transform 120ms ease;
}

.select-popover-enter-from,
.select-popover-leave-to {
  opacity: 0;
}

.select-popover-enter-from .select-menu-menu,
.select-popover-leave-to .select-menu-menu {
  transform: translateY(4px);
}

.select-sheet-enter-active,
.select-sheet-leave-active {
  transition: opacity 220ms ease;
}

.select-sheet-enter-active .select-menu-menu,
.select-sheet-leave-active .select-menu-menu {
  transition: transform 220ms cubic-bezier(0.32, 0.72, 0, 1);
}

.select-sheet-enter-from,
.select-sheet-leave-to {
  opacity: 0;
}

.select-sheet-enter-from .select-menu-menu,
.select-sheet-leave-to .select-menu-menu {
  transform: translateY(100%);
}

/* Matches MOBILE_MEDIA_QUERY in useMediaQuery.ts: bigger triggers for touch. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .select-menu {
    min-height: 30px;
    padding: 4px 8px 4px 10px;
    font-size: 0.86rem;
    border-radius: var(--radius-lg);
  }

  .is-field .select-menu,
  .is-field.mono .select-menu {
    min-height: 44px;
    padding: var(--space-2) var(--space-3);
    font-size: 1rem;
    border-radius: var(--radius-md);
  }
}

@media (prefers-reduced-motion: reduce) {
  .select-sheet-enter-active,
  .select-sheet-leave-active,
  .select-sheet-enter-active .select-menu-menu,
  .select-sheet-leave-active .select-menu-menu {
    transition-duration: 0ms;
  }
}
</style>
