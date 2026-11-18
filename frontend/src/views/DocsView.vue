<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { docsApi, type DocPage, type DocPageInfo } from '@/api/docs'
import { renderDoc } from '@/utils/docs'

// The documentation: a sidebar of pages, each with its own URL, and the open
// page's sections beneath it. Pages are Markdown the server embeds (docs/).
const route = useRoute()
const router = useRouter()

const pages = ref<DocPageInfo[]>([])
const page = ref<DocPage | null>(null)
const error = ref('')
const loading = ref(true)
const content = ref<HTMLElement | null>(null)
const scroller = ref<HTMLElement | null>(null)
const nav = ref<HTMLElement | null>(null)

// The open page names the tab, and leaving the docs gives the tab back the
// title it had.
const appTitle = document.title
let left = false
onBeforeUnmount(() => {
  left = true
  document.title = appTitle
})

const slug = computed(() => (typeof route.params.slug === 'string' ? route.params.slug : ''))
const html = computed(() => (page.value ? renderDoc(page.value.body) : ''))
const sections = computed(() => page.value?.sections.filter((s) => s.level === 2) ?? [])
const index = computed(() => pages.value.findIndex((p) => p.slug === slug.value))
const previous = computed(() => (index.value > 0 ? pages.value[index.value - 1] : null))
const next = computed(() => (index.value >= 0 && index.value < pages.value.length - 1 ? pages.value[index.value + 1] : null))

onMounted(async () => {
  try {
    pages.value = (await docsApi.list()).pages
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load the documentation'
    loading.value = false
    return
  }
  if (!slug.value && pages.value.length > 0) {
    await router.replace({ name: 'docs', params: { slug: pages.value[0].slug }, hash: route.hash })
  }
})

watch(
  slug,
  async (s) => {
    if (!s) return
    loading.value = true
    error.value = ''
    let p: DocPage | null = null
    let failure = ''
    try {
      p = await docsApi.get(s)
    } catch (e) {
      failure = e instanceof Error && e.message !== 'no such page' ? e.message : 'There is no such page.'
    }
    // A page that arrives after the docs were left, or after another was
    // asked for, is dropped.
    if (left || s !== slug.value) return
    page.value = p
    error.value = failure
    loading.value = false
    document.title = p ? `${p.title} · Devpad docs` : appTitle
    await nextTick()
    scrollToHash(true)
    nav.value?.querySelector('.active')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  },
  { immediate: true },
)

watch(
  () => route.hash,
  () => scrollToHash(false),
)

// scrollToHash brings the linked section into view, or the top of a newly
// opened page when the link names none.
function scrollToHash(newPage: boolean) {
  const id = decodeURIComponent(route.hash.slice(1))
  const target = id ? document.getElementById(id) : null
  if (target && content.value?.contains(target)) {
    target.scrollIntoView({ block: 'start' })
  } else if (newPage) {
    scroller.value?.scrollTo({ top: 0 })
  }
}

// Links between pages stay in the app; links elsewhere open a new tab.
function onContentClick(event: MouseEvent) {
  if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
  const link = (event.target as HTMLElement | null)?.closest('a')
  const href = link?.getAttribute('href')
  if (!link || !href) return
  if (href.startsWith('#')) {
    event.preventDefault()
    void router.push({ hash: href })
  } else if (href.startsWith('/') && !href.startsWith('//')) {
    event.preventDefault()
    void router.push(href)
  } else if (/^https?:\/\//.test(href)) {
    event.preventDefault()
    window.open(href, '_blank', 'noopener,noreferrer')
  }
}
</script>

<template>
  <div ref="scroller" class="docs-page">
    <div class="docs-layout">
      <aside class="docs-sidebar">
        <h1 class="docs-title">Docs</h1>
        <nav ref="nav" class="docs-nav" aria-label="Documentation">
          <template v-for="p in pages" :key="p.slug">
            <RouterLink
              :to="{ name: 'docs', params: { slug: p.slug } }"
              class="nav-item"
              :class="{ active: p.slug === slug }"
            >
              {{ p.title }}
            </RouterLink>
            <div v-if="p.slug === slug && sections.length > 0" class="nav-sections">
              <RouterLink
                v-for="s in sections"
                :key="s.id"
                :to="{ name: 'docs', params: { slug: p.slug }, hash: `#${s.id}` }"
                class="nav-section"
                :class="{ active: route.hash === `#${s.id}` }"
              >
                {{ s.title }}
              </RouterLink>
            </div>
          </template>
        </nav>
      </aside>

      <article class="docs-content">
        <p v-if="error" class="docs-error">{{ error }}</p>
        <template v-else-if="page">
          <header class="doc-header">
            <span v-if="page.admin" class="doc-badge">For admins</span>
            <h1 class="doc-title">{{ page.title }}</h1>
            <p class="doc-summary">{{ page.summary }}</p>
          </header>
          <div ref="content" class="doc-body" @click="onContentClick" v-html="html" />
          <nav class="doc-pager" aria-label="Pages">
            <RouterLink v-if="previous" :to="{ name: 'docs', params: { slug: previous.slug } }" class="pager-link">
              <span class="pager-label">Previous</span>
              <span class="pager-title">{{ previous.title }}</span>
            </RouterLink>
            <span v-else />
            <RouterLink v-if="next" :to="{ name: 'docs', params: { slug: next.slug } }" class="pager-link pager-next">
              <span class="pager-label">Next</span>
              <span class="pager-title">{{ next.title }}</span>
            </RouterLink>
          </nav>
        </template>
        <p v-else-if="loading" class="docs-loading">Loading…</p>
      </article>
    </div>
  </div>
</template>

<style scoped>
.docs-page {
  height: 100%;
  overflow-y: auto;
  padding: var(--space-6);
}

.docs-layout {
  display: flex;
  align-items: flex-start;
  gap: var(--space-8);
  max-width: 1040px;
  margin: 0 auto;
}

.docs-sidebar {
  position: sticky;
  top: 0;
  flex: 0 0 210px;
}

.docs-title {
  font-size: 1.5rem;
  font-weight: 500;
  color: var(--text-primary);
  margin-bottom: var(--space-4);
}

.docs-nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.nav-item {
  padding: 6px var(--space-3);
  font-size: 0.875rem;
  color: var(--text-secondary);
  text-decoration: none;
  border-radius: var(--radius-md);
  transition:
    background var(--transition-fast),
    color var(--transition-fast);
}

.nav-item:hover {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.nav-item.active {
  color: var(--accent);
  background: var(--bg-selected);
  font-weight: 500;
}

.nav-sections {
  display: flex;
  flex-direction: column;
  margin: 2px 0 var(--space-2) var(--space-3);
  padding-left: var(--space-2);
  border-left: 0.5px solid var(--border-subtle);
}

.nav-section {
  padding: 3px var(--space-2);
  font-size: 0.8rem;
  color: var(--text-tertiary);
  text-decoration: none;
  border-radius: var(--radius-sm);
}

.nav-section:hover {
  color: var(--text-primary);
}

.nav-section.active {
  color: var(--accent);
}

.docs-content {
  flex: 1;
  min-width: 0;
  max-width: 760px;
  padding-top: 2px;
}

.docs-error,
.docs-loading {
  color: var(--text-secondary);
  font-size: 0.9rem;
}

.doc-header {
  margin-bottom: var(--space-6);
  padding-bottom: var(--space-4);
  border-bottom: 0.5px solid var(--border-subtle);
}

.doc-badge {
  display: inline-block;
  margin-bottom: var(--space-2);
  padding: 1px var(--space-2);
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--warning);
  background: var(--warning-bg);
  border: 0.5px solid var(--warning-border);
  border-radius: var(--radius-sm);
}

.doc-title {
  font-size: 1.6rem;
  font-weight: 500;
  color: var(--text-primary);
}

.doc-summary {
  margin-top: var(--space-2);
  font-size: 0.95rem;
  color: var(--text-secondary);
}

.doc-body {
  font-size: 0.9rem;
  line-height: 1.65;
  color: var(--text-primary);
}

.doc-body :deep(> * + *) {
  margin-top: var(--space-3);
}

.doc-body :deep(h2),
.doc-body :deep(h3) {
  position: relative;
  font-weight: 500;
  color: var(--text-primary);
  scroll-margin-top: var(--space-4);
}

.doc-body :deep(h2) {
  margin-top: var(--space-8);
  font-size: 1.2rem;
}

.doc-body :deep(h3) {
  margin-top: var(--space-6);
  font-size: 1rem;
}

.doc-body :deep(h2:first-child) {
  margin-top: 0;
}

.doc-body :deep(.doc-anchor) {
  position: absolute;
  left: -1.1em;
  width: 1em;
  color: var(--text-dim);
  text-decoration: none;
  opacity: 0;
  transition: opacity var(--transition-fast);
}

.doc-body :deep(h2:hover .doc-anchor),
.doc-body :deep(h3:hover .doc-anchor) {
  opacity: 1;
}

.doc-body :deep(a) {
  color: var(--accent);
  text-decoration: underline;
  text-decoration-color: var(--accent-border);
  text-underline-offset: 2px;
}

.doc-body :deep(a:hover) {
  text-decoration-color: var(--accent);
}

.doc-body :deep(ul),
.doc-body :deep(ol) {
  padding-left: 1.4em;
}

.doc-body :deep(ul) {
  list-style: disc;
}

.doc-body :deep(ol) {
  list-style: decimal;
}

.doc-body :deep(li + li) {
  margin-top: var(--space-1);
}

.doc-body :deep(strong) {
  font-weight: 600;
}

.doc-body :deep(code) {
  font-family: var(--font-mono);
  font-size: 0.85em;
  padding: 1px 4px;
  background: var(--bg-raised);
  border-radius: var(--radius-sm);
}

.doc-body :deep(pre) {
  padding: var(--space-3) var(--space-4);
  overflow-x: auto;
  background: var(--bg-void);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  line-height: 1.5;
}

.doc-body :deep(pre code) {
  padding: 0;
  background: none;
  font-size: 0.8rem;
}

.doc-body :deep(blockquote) {
  padding: var(--space-2) var(--space-4);
  color: var(--text-secondary);
  background: var(--bg-elevated);
  border-left: 2px solid var(--accent-border);
  border-radius: 0 var(--radius-md) var(--radius-md) 0;
}

.doc-body :deep(table) {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.85rem;
}

.doc-body :deep(th),
.doc-body :deep(td) {
  padding: var(--space-2) var(--space-3);
  text-align: left;
  vertical-align: top;
  border-bottom: 0.5px solid var(--border-subtle);
}

.doc-body :deep(th) {
  font-weight: 500;
  color: var(--text-secondary);
}

.doc-pager {
  display: flex;
  justify-content: space-between;
  gap: var(--space-4);
  margin-top: var(--space-8);
  padding-top: var(--space-4);
  border-top: 0.5px solid var(--border-subtle);
}

.pager-link {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: var(--space-2) var(--space-3);
  text-decoration: none;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  transition: border-color var(--transition-fast);
}

.pager-link:hover {
  border-color: var(--accent-border);
}

.pager-next {
  text-align: right;
}

.pager-label {
  font-size: 0.7rem;
  color: var(--text-tertiary);
}

.pager-title {
  font-size: 0.875rem;
  color: var(--accent);
}

/* On narrow screens the nav becomes a strip of tabs above the page. */
@media (max-width: 768px) {
  .docs-page {
    padding: var(--space-4) var(--space-3);
  }

  .docs-layout {
    flex-direction: column;
    align-items: stretch;
    gap: var(--space-4);
  }

  .docs-sidebar {
    position: static;
    flex: none;
    width: 100%;
  }

  .docs-title {
    margin-bottom: var(--space-3);
  }

  .docs-nav {
    flex-direction: row;
    overflow-x: auto;
    scrollbar-width: none;
    border-bottom: 0.5px solid var(--border-subtle);
  }

  .nav-sections {
    display: none;
  }

  .nav-item {
    flex-shrink: 0;
    white-space: nowrap;
    padding: var(--space-2) var(--space-3);
    border-radius: var(--radius-md) var(--radius-md) 0 0;
  }

  .nav-item.active {
    background: none;
    box-shadow: inset 0 -2px 0 var(--accent);
  }

  .doc-body :deep(.doc-anchor) {
    display: none;
  }
}
</style>
