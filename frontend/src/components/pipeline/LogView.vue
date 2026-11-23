<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import type { LogLine } from '@/utils/logLines'

// Lines of output that nothing trusts, such as a pipeline job's, as plain
// text: each segment is a text node, coloured only through the theme's
// terminal colours. Only the lines in view are in the page, so scrolling
// stays smooth through a megabyte of output, and it follows the end while
// the reader is at the end.
const props = defineProps<{
  lines: readonly LogLine[]
  // The number of the first line, after lines dropped before it.
  firstLine?: number
}>()

// Every line is one row of this height: lines don't wrap, and the log
// scrolls sideways instead.
const LINE_PX = 18
const OVERSCAN = 30
const MAX_HEIGHT_PX = 440

const scroller = ref<HTMLElement | null>(null)
const scrollTop = ref(0)
const viewport = ref(MAX_HEIGHT_PX)
const following = ref(true)

const start = computed(() => Math.max(0, Math.floor(scrollTop.value / LINE_PX) - OVERSCAN))
const end = computed(() => Math.min(props.lines.length, Math.ceil((scrollTop.value + viewport.value) / LINE_PX) + OVERSCAN))
const visible = computed(() => props.lines.slice(start.value, end.value))
const numberWidth = computed(() => `${String((props.firstLine ?? 1) + props.lines.length).length + 1}ch`)

function onScroll() {
  const el = scroller.value
  if (!el) return
  scrollTop.value = el.scrollTop
  viewport.value = el.clientHeight
  following.value = el.scrollHeight - el.scrollTop - el.clientHeight < LINE_PX
}

function toEnd() {
  const el = scroller.value
  if (!el) return
  el.scrollTop = el.scrollHeight
  onScroll()
}

watch(
  () => props.lines.length,
  () => {
    if (following.value) void nextTick(toEnd)
  },
)

let resize: ResizeObserver | null = null
onMounted(() => {
  if (scroller.value) {
    resize = new ResizeObserver(onScroll)
    resize.observe(scroller.value)
  }
  void nextTick(toEnd)
})
onUnmounted(() => resize?.disconnect())
</script>

<template>
  <div ref="scroller" class="log" :style="{ maxHeight: `${MAX_HEIGHT_PX}px` }" tabindex="0" role="log" aria-label="Output" @scroll.passive="onScroll">
    <div class="log-track" :style="{ height: `${lines.length * LINE_PX}px`, '--num-width': numberWidth }">
      <div class="log-window" :style="{ transform: `translateY(${start * LINE_PX}px)` }">
        <div v-for="(line, i) in visible" :key="line.id" class="log-line">
          <span class="log-num" aria-hidden="true">{{ (firstLine ?? 1) + start + i }}</span>
          <span v-for="(seg, j) in line.segments" :key="j" :class="seg.cls">{{ seg.text }}</span>
          <span v-if="line.cut" class="log-cut" title="The rest of this line is not shown">…</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.log {
  overflow: auto;
  padding: 8px 0;
  background: var(--terminal-bg);
  border: 0.5px solid var(--border-hairline);
  border-radius: var(--radius-md);
  color: var(--term-fg);
  font-family: var(--font-mono);
  font-size: 0.72rem;
  overscroll-behavior: contain;
}

.log:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px var(--accent-glow), 0 0 0 1px var(--accent-border);
}

.log-track {
  position: relative;
  min-width: max-content;
}

.log-window {
  will-change: transform;
}

.log-line {
  height: 18px;
  line-height: 18px;
  padding-right: var(--space-3);
  white-space: pre;
}

.log-num {
  display: inline-block;
  width: var(--num-width);
  margin-right: var(--space-3);
  padding-left: var(--space-2);
  color: var(--term-bright-black);
  text-align: right;
  user-select: none;
}

.log-cut {
  color: var(--term-bright-black);
}

.ansi-bold { font-weight: 700; }
.ansi-black { color: var(--term-black); }
.ansi-red { color: var(--term-red); }
.ansi-green { color: var(--term-green); }
.ansi-yellow { color: var(--term-yellow); }
.ansi-blue { color: var(--term-blue); }
.ansi-magenta { color: var(--term-magenta); }
.ansi-cyan { color: var(--term-cyan); }
.ansi-white { color: var(--term-white); }
.ansi-bright-black { color: var(--term-bright-black); }
.ansi-bright-red { color: var(--term-bright-red); }
.ansi-bright-green { color: var(--term-bright-green); }
.ansi-bright-yellow { color: var(--term-bright-yellow); }
.ansi-bright-blue { color: var(--term-bright-blue); }
.ansi-bright-magenta { color: var(--term-bright-magenta); }
.ansi-bright-cyan { color: var(--term-bright-cyan); }
.ansi-bright-white { color: var(--term-bright-white); }
</style>
