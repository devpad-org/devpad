<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { aiApi, type UsagePeriod, type UsageProject, type UsageReport, type UsageTotals } from '@/api/ai'
import { useAuthStore } from '@/stores/auth'
import UsageChart, { type ChartColumn, type ChartSeries } from '@/components/usage/UsageChart.vue'
import { formatCost, formatCount, formatTokens } from '@/components/usage/format'

const auth = useAuthStore()

const SERIES_SLOTS = 8
const OTHER = '\u0000other'

const spans: Record<UsagePeriod, { counts: number[]; initial: number; unit: string }> = {
  day: { counts: [7, 30, 90], initial: 30, unit: 'days' },
  week: { counts: [4, 12, 26, 52], initial: 12, unit: 'weeks' },
  month: { counts: [3, 6, 12, 24], initial: 12, unit: 'months' },
}
const periodNames: Record<UsagePeriod, string> = { day: 'Daily', week: 'Weekly', month: 'Monthly' }
const perPeriod: Record<UsagePeriod, string> = { day: 'day', week: 'week', month: 'month' }

const period = ref<UsagePeriod>('day')
const span = ref(spans.day.initial)
const projectFilter = ref<number | 'all'>('all')
const everyone = ref(false)

const report = ref<UsageReport | null>(null)
const loading = ref(false)
const error = ref('')
// Projects seen in any report, so a chosen project stays in the list when a
// shorter range has no usage for it.
const knownProjects = ref(new Map<number, UsageProject>())

watch(period, (p) => {
  span.value = spans[p].initial
})

function isoDate(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

function parseDate(iso: string): Date {
  const [y, m, d] = iso.split('-').map(Number)
  return new Date(y, m - 1, d)
}

// The range ends today; the server widens it to whole periods.
function rangeStart(p: UsagePeriod, count: number, today: Date): Date {
  switch (p) {
    case 'week':
      return new Date(today.getFullYear(), today.getMonth(), today.getDate() - 7 * (count - 1))
    case 'month':
      return new Date(today.getFullYear(), today.getMonth() - (count - 1), 1)
    default:
      return new Date(today.getFullYear(), today.getMonth(), today.getDate() - (count - 1))
  }
}

let loadSeq = 0
async function load() {
  const seq = ++loadSeq
  loading.value = true
  error.value = ''
  const today = new Date()
  try {
    const result = await aiApi.getUsage({
      period: period.value,
      from: isoDate(rangeStart(period.value, span.value, today)),
      to: isoDate(today),
      tz: Intl.DateTimeFormat().resolvedOptions().timeZone,
      everyone: everyone.value,
    })
    if (seq !== loadSeq) return
    report.value = result
    for (const p of result.projects) knownProjects.value.set(p.id, p)
  } catch (e) {
    if (seq !== loadSeq) return
    error.value = e instanceof Error ? e.message : 'Failed to load usage'
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

watch([period, span, everyone], load, { immediate: true })
watch(everyone, () => {
  knownProjects.value = new Map()
  projectFilter.value = 'all'
})

const projectOptions = computed(() =>
  [...knownProjects.value.values()].sort((a, b) => projectName(a).localeCompare(projectName(b))),
)

// projectLabel names a project; projectName also says when it was deleted.
function projectLabel(p: UsageProject): string {
  return p.id === 0 ? 'No project' : p.name
}

function projectName(p: UsageProject): string {
  return p.deleted ? `${projectLabel(p)} (deleted)` : projectLabel(p)
}

const rows = computed(() => {
  const all = report.value?.rows ?? []
  return projectFilter.value === 'all' ? all : all.filter((r) => r.projectId === projectFilter.value)
})

// Colors go to models in the report's order, costliest first, so filtering
// by project never repaints a model.
const seriesOf = computed(() => {
  const models = report.value?.models ?? []
  const folds = models.length > SERIES_SLOTS
  const keep = folds ? SERIES_SLOTS - 1 : SERIES_SLOTS
  const byModel = new Map<string, ChartSeries>()
  const list: ChartSeries[] = []
  models.forEach((m, i) => {
    if (i < keep) {
      const s = { id: m.id, name: m.name, color: `var(--series-${i + 1})` }
      byModel.set(m.id, s)
      list.push(s)
    }
  })
  if (folds) {
    const other = { id: OTHER, name: `${models.length - keep} other models`, color: 'var(--series-other)' }
    for (const m of models.slice(keep)) byModel.set(m.id, other)
    list.push(other)
  }
  return { byModel, list }
})

const shownSeries = computed(() => {
  const present = new Set(rows.value.filter((r) => r.costUsd > 0).map((r) => seriesOf.value.byModel.get(r.model)?.id))
  return seriesOf.value.list.filter((s) => present.has(s.id))
})

const dayFormat = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' })
const weekdayFormat = new Intl.DateTimeFormat(undefined, { weekday: 'short', month: 'short', day: 'numeric' })
const monthFormat = new Intl.DateTimeFormat(undefined, { month: 'short' })
const monthYearFormat = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' })

const columns = computed<ChartColumn[]>(() => {
  const r = report.value
  if (!r) return []
  const values = new Map<string, Record<string, number>>()
  for (const row of rows.value) {
    const id = seriesOf.value.byModel.get(row.model)?.id ?? row.model
    const v = values.get(row.period) ?? {}
    v[id] = (v[id] ?? 0) + row.costUsd
    values.set(row.period, v)
  }
  return r.periods.map((p) => {
    const d = parseDate(p)
    const label = r.period === 'month' ? monthFormat.format(d) : dayFormat.format(d)
    const title =
      r.period === 'month' ? monthYearFormat.format(d) : r.period === 'week' ? `Week of ${dayFormat.format(d)}` : weekdayFormat.format(d)
    return { label, title, values: values.get(p) ?? {} }
  })
})

function emptyTotals(): UsageTotals {
  return { requests: 0, unpricedRequests: 0, inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, costUsd: 0 }
}

function add(to: UsageTotals, from: UsageTotals) {
  to.requests += from.requests
  to.unpricedRequests += from.unpricedRequests
  to.inputTokens += from.inputTokens
  to.outputTokens += from.outputTokens
  to.cacheReadTokens += from.cacheReadTokens
  to.cacheWriteTokens += from.cacheWriteTokens
  to.costUsd += from.costUsd
}

function tokens(t: UsageTotals): number {
  return t.inputTokens + t.outputTokens + t.cacheReadTokens + t.cacheWriteTokens
}

const totals = computed(() => {
  const t = emptyTotals()
  for (const row of rows.value) add(t, row)
  return t
})

const average = computed(() => {
  const n = report.value?.periods.length ?? 0
  return n > 0 ? totals.value.costUsd / n : 0
})

// The table: each project, costliest first, with its models.
const byProject = computed(() => {
  const r = report.value
  if (!r) return []
  const models = new Map(r.models.map((m) => [m.id, m.name]))
  const known = new Map(r.projects.map((p) => [p.id, p]))
  const projects = new Map<number, { project: UsageProject; totals: UsageTotals; models: Map<string, UsageTotals> }>()
  for (const row of rows.value) {
    let entry = projects.get(row.projectId)
    if (!entry) {
      const project = known.get(row.projectId) ?? { id: row.projectId, name: '', deleted: false }
      entry = { project, totals: emptyTotals(), models: new Map() }
      projects.set(row.projectId, entry)
    }
    add(entry.totals, row)
    const m = entry.models.get(row.model) ?? emptyTotals()
    add(m, row)
    entry.models.set(row.model, m)
  }
  return [...projects.values()]
    .sort((a, b) => b.totals.costUsd - a.totals.costUsd || b.totals.requests - a.totals.requests)
    .map((e) => ({
      ...e,
      models: [...e.models.entries()]
        .map(([id, t]) => ({ id, name: models.get(id) ?? id, color: seriesOf.value.byModel.get(id)?.color ?? 'var(--series-other)', totals: t }))
        .sort((a, b) => b.totals.costUsd - a.totals.costUsd || b.totals.requests - a.totals.requests),
    }))
})
</script>

<template>
  <div class="page usage-page">
    <div class="page-header">
      <div>
        <h1 class="page-title">Usage</h1>
        <p class="page-subtitle">What AI requests cost, by project and model</p>
      </div>
    </div>

    <div class="filters">
      <div class="segmented" role="radiogroup" aria-label="Period">
        <button
          v-for="p in (['day', 'week', 'month'] as UsagePeriod[])"
          :key="p"
          type="button"
          role="radio"
          class="segment"
          :class="{ active: period === p }"
          :aria-checked="period === p"
          @click="period = p"
        >
          {{ periodNames[p] }}
        </button>
      </div>
      <select v-model.number="span" class="form-input filter-select" aria-label="Range">
        <option v-for="n in spans[period].counts" :key="n" :value="n">Last {{ n }} {{ spans[period].unit }}</option>
      </select>
      <select v-model="projectFilter" class="form-input filter-select" aria-label="Project">
        <option value="all">All projects</option>
        <option v-for="p in projectOptions" :key="p.id" :value="p.id">{{ projectName(p) }}</option>
      </select>
      <div v-if="auth.isAdmin" class="segmented" role="radiogroup" aria-label="Whose usage">
        <button type="button" role="radio" class="segment" :class="{ active: !everyone }" :aria-checked="!everyone" @click="everyone = false">
          Mine
        </button>
        <button type="button" role="radio" class="segment" :class="{ active: everyone }" :aria-checked="everyone" @click="everyone = true">
          Everyone
        </button>
      </div>
    </div>

    <div v-if="error" class="notice notice-error">{{ error }}</div>

    <div v-if="report" class="usage-content" :class="{ refreshing: loading }">
      <div class="stats">
        <div class="stat stat-hero">
          <div class="stat-label">Total cost</div>
          <div class="stat-value">{{ formatCost(totals.costUsd) }}</div>
        </div>
        <div class="stat">
          <div class="stat-label">Average per {{ perPeriod[report.period] }}</div>
          <div class="stat-value">{{ formatCost(average) }}</div>
        </div>
        <div class="stat">
          <div class="stat-label">Requests</div>
          <div class="stat-value">{{ formatCount(totals.requests) }}</div>
        </div>
        <div class="stat">
          <div class="stat-label">Tokens</div>
          <div class="stat-value">{{ formatTokens(tokens(totals)) }}</div>
        </div>
      </div>

      <div v-if="totals.unpricedRequests > 0" class="notice notice-warning">
        {{ formatCount(totals.unpricedRequests) }} {{ totals.unpricedRequests === 1 ? 'request was' : 'requests were' }} to models
        devpad has no prices for, so {{ totals.unpricedRequests === 1 ? 'it is' : 'they are' }} not in the cost. Their tokens are in the
        table.
      </div>

      <section class="panel section">
        <div class="section-header">
          <h2 class="section-title">Cost per {{ perPeriod[report.period] }}</h2>
        </div>
        <template v-if="totals.costUsd > 0">
          <ul class="legend" aria-label="Models">
            <li v-for="s in shownSeries" :key="s.id">
              <span class="legend-swatch" :style="{ background: s.color }" />
              {{ s.name }}
            </li>
          </ul>
          <UsageChart :series="shownSeries" :columns="columns" />
        </template>
        <p v-else class="empty-text">No priced requests in this range.</p>
      </section>

      <section class="panel section">
        <div class="section-header">
          <h2 class="section-title">By project</h2>
        </div>
        <p v-if="byProject.length === 0" class="empty-text">No requests in this range.</p>
        <div v-else class="table-scroll">
          <table class="usage-table">
            <thead>
              <tr>
                <th scope="col">Project / model</th>
                <th scope="col" class="num">Requests</th>
                <th scope="col" class="num">Input</th>
                <th scope="col" class="num">Output</th>
                <th scope="col" class="num">Cache read</th>
                <th scope="col" class="num">Cache write</th>
                <th scope="col" class="num">Cost</th>
              </tr>
            </thead>
            <tbody v-for="entry in byProject" :key="entry.project.id">
              <tr class="project-row">
                <th scope="row">
                  {{ projectLabel(entry.project) }}
                  <span v-if="entry.project.deleted" class="muted">(deleted)</span>
                </th>
                <td class="num">{{ formatCount(entry.totals.requests) }}</td>
                <td class="num">{{ formatTokens(entry.totals.inputTokens) }}</td>
                <td class="num">{{ formatTokens(entry.totals.outputTokens) }}</td>
                <td class="num">{{ formatTokens(entry.totals.cacheReadTokens) }}</td>
                <td class="num">{{ formatTokens(entry.totals.cacheWriteTokens) }}</td>
                <td class="num">{{ formatCost(entry.totals.costUsd) }}</td>
              </tr>
              <tr v-for="m in entry.models" :key="m.id" class="model-row">
                <th scope="row">
                  <span class="legend-swatch" :style="{ background: m.color }" />
                  {{ m.name }}
                </th>
                <td class="num">{{ formatCount(m.totals.requests) }}</td>
                <td class="num">{{ formatTokens(m.totals.inputTokens) }}</td>
                <td class="num">{{ formatTokens(m.totals.outputTokens) }}</td>
                <td class="num">{{ formatTokens(m.totals.cacheReadTokens) }}</td>
                <td class="num">{{ formatTokens(m.totals.cacheWriteTokens) }}</td>
                <td class="num">
                  <template v-if="m.totals.unpricedRequests === m.totals.requests">
                    <span class="muted" title="devpad has no prices for this model">—</span>
                  </template>
                  <template v-else>{{ formatCost(m.totals.costUsd) }}</template>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
    <p v-else-if="loading" class="empty-text">Loading usage…</p>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="@/assets/styles/segmented.css"></style>
<style scoped>
.usage-page {
  max-width: 1000px;
}

.filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-4);
}

.filter-select {
  width: auto;
  min-width: 140px;
  max-width: 100%;
  padding-top: 5px;
  padding-bottom: 5px;
  font-size: 0.8rem;
}

/* As tall as the selects beside it. */
.segment {
  padding-block: var(--space-1);
}

.usage-content {
  transition: opacity var(--transition-normal);
}

/* A reload keeps the last report on screen, faded, until the new one comes. */
.usage-content.refreshing {
  opacity: 0.5;
}

.stats {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: var(--space-3);
  margin-bottom: var(--space-4);
}

.stat {
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  padding: var(--space-3) var(--space-4);
}

.stat-label {
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.stat-value {
  font-size: 1.25rem;
  font-weight: 600;
}

.stat-hero .stat-value {
  font-size: 2rem;
  line-height: 1.2;
}

.notice {
  margin-bottom: var(--space-4);
}

.legend {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-4);
  list-style: none;
  margin-bottom: var(--space-3);
  font-size: 0.78rem;
  color: var(--text-secondary);
}

.legend li {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.legend-swatch {
  display: inline-block;
  width: 10px;
  height: 10px;
  border-radius: 2px;
  flex-shrink: 0;
}

.table-scroll {
  overflow-x: auto;
}

.usage-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.8rem;
}

.usage-table th,
.usage-table td {
  padding: var(--space-1) var(--space-2);
  text-align: left;
  white-space: nowrap;
}

.usage-table thead th {
  font-weight: 500;
  font-size: 0.72rem;
  color: var(--text-tertiary);
  border-bottom: 0.5px solid var(--border-default);
}

.usage-table .num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.project-row th,
.project-row td {
  font-weight: 500;
  border-top: 0.5px solid var(--border-subtle);
}

.project-row th {
  white-space: normal;
  overflow-wrap: anywhere;
  min-width: 140px;
}

.model-row th {
  font-weight: 400;
  color: var(--text-secondary);
  padding-left: var(--space-5);
}

.model-row th .legend-swatch {
  margin-right: var(--space-1);
  vertical-align: -1px;
}

.model-row td {
  color: var(--text-secondary);
}
</style>
