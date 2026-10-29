<script setup lang="ts">
import { computed } from 'vue'
import { EMBLEM_CELL, projectEmblem } from '@/utils/projectEmblem'

// The project's generated emblem; see utils/projectEmblem.ts.

const props = withDefaults(defineProps<{ name: string; size?: number }>(), { size: 44 })

const emblem = computed(() => projectEmblem(props.name))
const half = EMBLEM_CELL / 2
const BAR = EMBLEM_CELL / 5
</script>

<template>
  <svg
    class="project-emblem"
    :width="size"
    :height="size"
    :viewBox="`0 0 ${EMBLEM_CELL * 2} ${EMBLEM_CELL * 2}`"
    aria-hidden="true"
    focusable="false"
  >
    <g v-for="(cell, i) in emblem.cells" :key="i" :transform="`translate(${cell.x} ${cell.y})`">
      <rect :width="EMBLEM_CELL" :height="EMBLEM_CELL" :fill="cell.background" />
      <g :transform="`rotate(${cell.rotation * 90} ${half} ${half})`" :fill="cell.foreground">
        <path v-if="cell.shape === 'quarter'" :d="`M0 0H${EMBLEM_CELL}A${EMBLEM_CELL} ${EMBLEM_CELL} 0 0 1 0 ${EMBLEM_CELL}Z`" />
        <path v-else-if="cell.shape === 'half'" :d="`M${EMBLEM_CELL} 0A${half} ${half} 0 0 1 0 0Z`" />
        <circle v-else-if="cell.shape === 'circle'" :cx="half" :cy="half" :r="half - 2" />
        <template v-else-if="cell.shape === 'ring'">
          <circle :cx="half" :cy="half" :r="half - 1" />
          <circle :cx="half" :cy="half" :r="half / 2.4" :fill="cell.background" />
        </template>
        <path v-else-if="cell.shape === 'triangle'" :d="`M0 0H${EMBLEM_CELL}L0 ${EMBLEM_CELL}Z`" />
        <path
          v-else-if="cell.shape === 'leaf'"
          :d="`M0 ${EMBLEM_CELL}A${EMBLEM_CELL} ${EMBLEM_CELL} 0 0 1 ${EMBLEM_CELL} 0A${EMBLEM_CELL} ${EMBLEM_CELL} 0 0 1 0 ${EMBLEM_CELL}Z`"
        />
        <template v-else>
          <rect v-for="n in 3" :key="n" :y="(n - 1) * BAR * 2" :width="EMBLEM_CELL" :height="BAR" />
        </template>
      </g>
    </g>
  </svg>
</template>

<style scoped>
.project-emblem {
  display: block;
  flex-shrink: 0;
  border-radius: 22%;
  overflow: hidden;
  outline: 0.5px solid var(--border-subtle);
  outline-offset: -0.5px;
}
</style>
