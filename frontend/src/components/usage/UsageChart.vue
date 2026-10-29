<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { formatCost, formatAxisCost } from './format'

// Stacked columns of cost per period, one segment per series. Series come
// bottom first; each keeps the color it is given, whatever is filtered.
export interface ChartSeries {
  id: string
  name: string
  color: string
}

export interface ChartColumn {
  label: string
  // The tooltip's heading, such as "Week of Oct 26".
  title: string
  // Cost by series id.
  values: Record<string, number>
}

const props = defineProps<{
  series: ChartSeries[]
  columns: ChartColumn[]
}>()

const HEIGHT = 240
const MARGIN = { top: 20, right: 8, bottom: 24, left: 52 }
const GAP = 2
const RADIUS = 4
const MAX_BAR = 24
// About how wide an axis label is, to skip labels that would collide.
const LABEL_WIDTH = 48

const root = ref<HTMLElement | null>(null)
const width = ref(640)
let observer: ResizeObserver | null = null

onMounted(() => {
  if (!root.value) return
  width.value = root.value.clientWidth || width.value
  observer = new ResizeObserver(([entry]) => {
    width.value = Math.max(200, Math.floor(entry.contentRect.width))
  })
  observer.observe(root.value)
})
onBeforeUnmount(() => observer?.disconnect())

const plotWidth = computed(() => width.value - MARGIN.left - MARGIN.right)
const plotHeight = HEIGHT - MARGIN.top - MARGIN.bottom
const band = computed(() => plotWidth.value / Math.max(1, props.columns.length))
const barWidth = computed(() => Math.max(2, Math.min(MAX_BAR, band.value * 0.6)))

const totals = computed(() => props.columns.map((c) => props.series.reduce((sum, s) => sum + (c.values[s.id] ?? 0), 0)))

// A clean top for the axis, and the ticks up to it.
const scale = computed(() => {
  const max = Math.max(0, ...totals.value)
  if (max <= 0) return { top: 1, ticks: [0, 1] }
  const rough = max / 4
  const magnitude = 10 ** Math.floor(Math.log10(rough))
  const step = [1, 2, 2.5, 5, 10].map((m) => m * magnitude).find((s) => s >= rough) ?? 10 * magnitude
  const top = Math.ceil(max / step) * step
  const ticks: number[] = []
  for (let v = 0; v <= top + step / 2; v += step) ticks.push(v)
  return { top, ticks }
})

function y(value: number): number {
  return MARGIN.top + plotHeight - (value / scale.value.top) * plotHeight
}

function centerX(i: number): number {
  return MARGIN.left + band.value * (i + 0.5)
}

interface Segment {
  key: string
  color: string
  path: string
}

// Each segment sits on the one below with a gap of surface between them;
// only the top of the column is rounded.
const segments = computed(() =>
  props.columns.map((column, i) => {
    const x = centerX(i) - barWidth.value / 2
    const stack = props.series
      .map((s) => ({ s, value: column.values[s.id] ?? 0 }))
      .filter((p) => p.value > 0)
    const out: Segment[] = []
    let base = 0
    stack.forEach(({ s, value }, k) => {
      const bottom = y(base)
      base += value
      const top = y(base)
      const isTop = k === stack.length - 1
      const h = bottom - top - (isTop ? 0 : GAP)
      if (h < 0.5) return
      out.push({ key: s.id, color: s.color, path: isTop ? roundedTop(x, bottom - h, barWidth.value, h) : rect(x, bottom - h, barWidth.value, h) })
    })
    return out
  }),
)

function rect(x: number, top: number, w: number, h: number): string {
  return `M${x},${top}h${w}v${h}h${-w}Z`
}

function roundedTop(x: number, top: number, w: number, h: number): string {
  const r = Math.min(RADIUS, h, w / 2)
  return `M${x},${top + h}V${top + r}Q${x},${top} ${x + r},${top}H${x + w - r}Q${x + w},${top} ${x + w},${top + r}V${top + h}Z`
}

const labelEvery = computed(() => Math.max(1, Math.ceil(LABEL_WIDTH / band.value)))

// Only the costliest column carries its total; the axis, tooltip and table
// carry the rest.
const peak = computed(() => {
  let best = -1
  totals.value.forEach((t, i) => {
    if (t > 0 && (best < 0 || t > totals.value[best])) best = i
  })
  return best
})

const active = ref<number | null>(null)

const tooltip = computed(() => {
  const i = active.value
  if (i === null) return null
  const column = props.columns[i]
  const rows = props.series
    .map((s) => ({ ...s, value: column.values[s.id] ?? 0 }))
    .filter((r) => r.value > 0)
    .reverse()
  const left = centerX(i)
  return {
    title: column.title,
    total: totals.value[i],
    rows,
    // Keep the tooltip inside the chart.
    style: left > width.value / 2 ? { right: `${width.value - left + barWidth.value}px` } : { left: `${left + barWidth.value}px` },
  }
})

function columnLabel(i: number): string {
  return `${props.columns[i].title}: ${formatCost(totals.value[i])}`
}
</script>

<template>
  <div ref="root" class="usage-chart" @pointerleave="active = null">
    <svg :width="width" :height="HEIGHT" role="group" aria-label="Cost per period, by model">
      <g class="grid">
        <g v-for="tick in scale.ticks" :key="tick">
          <line :x1="MARGIN.left" :x2="width - MARGIN.right" :y1="y(tick)" :y2="y(tick)" />
          <text :x="MARGIN.left - 8" :y="y(tick)" dy="0.32em" text-anchor="end">{{ formatAxisCost(tick) }}</text>
        </g>
      </g>
      <rect
        v-if="active !== null"
        class="hover-band"
        :x="MARGIN.left + band * active"
        :y="MARGIN.top"
        :width="band"
        :height="plotHeight"
      />
      <g v-for="(column, i) in segments" :key="i">
        <path v-for="seg in column" :key="seg.key" :d="seg.path" :style="{ fill: seg.color }" />
      </g>
      <text v-if="peak >= 0" class="cap-label" :x="centerX(peak)" :y="y(totals[peak]) - 6" text-anchor="middle">
        {{ formatCost(totals[peak]) }}
      </text>
      <g class="x-axis">
        <template v-for="(column, i) in columns" :key="i">
          <text v-if="i % labelEvery === 0" :x="centerX(i)" :y="HEIGHT - 6" text-anchor="middle">{{ column.label }}</text>
        </template>
      </g>
      <rect
        v-for="(_, i) in columns"
        :key="`hit-${i}`"
        class="hit"
        :x="MARGIN.left + band * i"
        :y="MARGIN.top"
        :width="band"
        :height="plotHeight"
        tabindex="0"
        :aria-label="columnLabel(i)"
        @pointerenter="active = i"
        @focus="active = i"
        @blur="active = null"
      />
    </svg>
    <div v-if="tooltip" class="chart-tooltip" :style="tooltip.style" role="status">
      <div class="tooltip-title">{{ tooltip.title }}</div>
      <div class="tooltip-total">{{ formatCost(tooltip.total) }}</div>
      <div v-for="row in tooltip.rows" :key="row.id" class="tooltip-row">
        <span class="tooltip-key" :style="{ background: row.color }" />
        <span class="tooltip-value">{{ formatCost(row.value) }}</span>
        <span class="tooltip-name">{{ row.name }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.usage-chart {
  position: relative;
  width: 100%;
}

svg {
  display: block;
  overflow: visible;
}

.grid line {
  stroke: var(--border-subtle);
  stroke-width: 1;
  shape-rendering: crispEdges;
}

.grid text,
.x-axis text {
  fill: var(--text-tertiary);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}

.cap-label {
  fill: var(--text-secondary);
  font-size: 11px;
  font-weight: 500;
}

.hover-band {
  fill: var(--bg-hover);
}

.hit {
  fill: transparent;
  outline: none;
}

.hit:focus-visible {
  stroke: var(--accent-border);
  stroke-width: 1;
}

.chart-tooltip {
  position: absolute;
  top: 8px;
  z-index: 2;
  min-width: 160px;
  max-width: 260px;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-raised);
  border: 0.5px solid var(--border-strong);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-popover);
  pointer-events: none;
  font-size: 0.75rem;
}

.tooltip-title {
  color: var(--text-secondary);
}

.tooltip-total {
  font-size: 0.95rem;
  font-weight: 600;
  margin-bottom: var(--space-1);
}

.tooltip-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  line-height: 1.6;
}

.tooltip-key {
  width: 10px;
  height: 2px;
  border-radius: 1px;
  flex-shrink: 0;
}

.tooltip-value {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.tooltip-name {
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
