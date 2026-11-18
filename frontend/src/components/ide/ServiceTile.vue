<script setup lang="ts">
import { computed } from 'vue'
import { serviceCatalogItem } from './serviceCatalog'
import ServiceLogo from './ServiceLogo.vue'

// A service type's logo on a square tinted with its theme hue, so a list of
// services can be told apart by colour at a glance. `muted` greys it out for
// a service that is stopped or can't be deployed.
const props = defineProps<{
  type: string
  muted?: boolean
}>()

const hue = computed(() => `var(${serviceCatalogItem(props.type).hue})`)
</script>

<template>
  <span class="service-tile" :class="{ muted }" :style="{ '--service-hue': hue }">
    <ServiceLogo :type="type" />
  </span>
</template>

<style scoped>
.service-tile {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 2em;
  height: 2em;
  font-size: 1rem;
  background: color-mix(in srgb, var(--service-hue) 13%, transparent);
  border: 0.5px solid color-mix(in srgb, var(--service-hue) 30%, transparent);
  border-radius: var(--radius-lg);
  transition: filter var(--transition-fast), opacity var(--transition-fast);
}

.service-tile :deep(.service-logo) {
  width: 1.1em;
  height: 1.1em;
}

.service-tile.muted {
  filter: grayscale(1);
  opacity: 0.6;
}
</style>
