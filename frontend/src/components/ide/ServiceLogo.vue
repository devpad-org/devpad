<script setup lang="ts">
import { computed } from 'vue'
import { serviceCatalogItem } from './serviceCatalog'

// A service type's logo, tinted with its theme hue. It is 1em square, so the
// font size of where it sits sets its size.
const props = defineProps<{ type: string }>()

const item = computed(() => serviceCatalogItem(props.type))
</script>

<template>
  <svg
    class="service-logo"
    :style="{ color: `var(${item.hue})` }"
    viewBox="0 0 24 24"
    :fill="item.logo.stroke ? 'none' : 'currentColor'"
    :stroke="item.logo.stroke ? 'currentColor' : 'none'"
    :stroke-width="item.logo.stroke ? 2 : undefined"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path v-for="d in item.logo.paths" :key="d" :d="d" />
  </svg>
</template>

<style scoped>
.service-logo {
  display: block;
  width: 1em;
  height: 1em;
  flex-shrink: 0;
}
</style>
