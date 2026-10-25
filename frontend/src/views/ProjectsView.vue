<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useProjectStore } from '@/stores/projects'
import ProjectModal from '@/components/project/ProjectModal.vue'
import StatusBadge from '@/components/project/StatusBadge.vue'
import type { ProjectInput } from '@/api/projects'

const store = useProjectStore()
const router = useRouter()

const showCreate = ref(false)
const saving = ref(false)
const createError = ref('')
let timer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  void store.fetchProjects().catch(() => {})
  // Projects take a while to provision; refresh until they are all settled.
  timer = setInterval(() => {
    if (store.projects.some((p) => p.status === 'creating')) {
      void store.fetchProjects().catch(() => {})
    }
  }, 3000)
})

onUnmounted(() => {
  if (timer !== null) clearInterval(timer)
})

async function handleCreate(input: ProjectInput) {
  saving.value = true
  createError.value = ''
  try {
    const project = await store.createProject(input)
    showCreate.value = false
    router.push({ name: 'project', params: { id: project.id } })
  } catch (e) {
    createError.value = e instanceof Error ? e.message : 'Failed to create project'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page projects-page">
    <div class="page-header">
      <div>
        <h1 class="page-title">Projects</h1>
        <p class="page-subtitle">Repositories that agents work on in isolated sandboxes</p>
      </div>
      <button class="btn btn-primary" @click="createError = ''; showCreate = true">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 5v14" />
          <path d="M5 12h14" />
        </svg>
        New Project
      </button>
    </div>

    <p v-if="store.loading && store.projects.length === 0" class="empty-text">Loading projects...</p>
    <p v-else-if="store.error" class="notice notice-error">{{ store.error }}</p>
    <div v-else-if="store.projects.length === 0" class="empty-state">
      <p class="empty-title">No projects yet</p>
      <p class="empty-text">Create a project from a repository, then give agents tasks to work on in parallel.</p>
    </div>

    <div v-else class="project-grid">
      <RouterLink
        v-for="p in store.projects"
        :key="p.id"
        :to="{ name: 'project', params: { id: p.id } }"
        class="panel project-card"
      >
        <div class="card-header">
          <h2 class="card-title">{{ p.name }}</h2>
          <StatusBadge :status="p.status" />
        </div>
        <p v-if="p.description" class="card-description">{{ p.description }}</p>
        <p v-if="p.status === 'error' && p.error" class="card-error">{{ p.error }}</p>
        <div class="meta-row">
          <span class="mono">{{ p.defaultBranch }}</span>
          <span>&middot;</span>
          <span class="mono card-upstream">{{ p.upstreamUrl || 'local only' }}</span>
          <span>&middot;</span>
          <span>{{ p.maxSandboxes }} sandbox{{ p.maxSandboxes === 1 ? '' : 'es' }}</span>
        </div>
      </RouterLink>
    </div>

    <ProjectModal
      :show="showCreate"
      :saving="saving"
      :error="createError"
      @close="showCreate = false"
      @submit="handleCreate"
    />
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped>
.projects-page {
  max-width: 960px;
}

.project-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(300px, 100%), 1fr));
  gap: var(--space-4);
}

.project-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  color: var(--text-primary);
  transition: border-color var(--transition-fast);
}

.project-card:hover {
  color: var(--text-primary);
  border-color: var(--border-active);
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.card-title {
  font-size: 0.95rem;
  font-weight: 500;
  overflow-wrap: anywhere;
}

.card-description {
  font-size: 0.8rem;
  color: var(--text-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.card-error {
  font-size: 0.75rem;
  color: var(--error);
  overflow-wrap: anywhere;
}

.card-upstream {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 100%;
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: var(--space-8) 0;
  text-align: center;
}

.empty-title {
  color: var(--text-secondary);
  font-size: 0.95rem;
}
</style>
