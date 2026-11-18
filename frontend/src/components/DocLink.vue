<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { DOC_LINKS, type DocTopic } from '@/utils/docs'

// A small "?" beside something the documentation explains. In the IDE, which
// has no app header to come back by, it opens the docs in a new tab so the
// workspace stays open.
const props = withDefaults(defineProps<{ topic: DocTopic; label?: string }>(), { label: '' })

const route = useRoute()
const href = computed(() => DOC_LINKS[props.topic])
const newTab = computed(() => route.meta.hideChrome === true)
const title = computed(() => (props.label ? `${props.label} in the docs` : 'Read about this in the docs'))
</script>

<template>
  <a
    v-if="newTab"
    :href="href"
    target="_blank"
    rel="noopener"
    class="doc-link"
    :title="title"
    :aria-label="title"
    @click.stop
  >
    <span aria-hidden="true">?</span>
  </a>
  <RouterLink v-else :to="href" class="doc-link" :title="title" :aria-label="title" @click.stop>
    <span aria-hidden="true">?</span>
  </RouterLink>
</template>

<style scoped>
.doc-link {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 15px;
  height: 15px;
  margin-left: var(--space-1);
  font-family: var(--font-sans);
  font-size: 0.65rem;
  font-weight: 600;
  line-height: 1;
  color: var(--text-tertiary);
  text-decoration: none;
  vertical-align: middle;
  border: 0.5px solid var(--border-strong);
  border-radius: 50%;
  transition:
    color var(--transition-fast),
    border-color var(--transition-fast);
}

.doc-link:hover,
.doc-link:focus-visible {
  color: var(--accent);
  border-color: var(--accent-border);
}
</style>
