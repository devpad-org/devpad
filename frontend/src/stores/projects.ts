import { defineStore } from 'pinia'
import { ref } from 'vue'
import { projectApi, type Project, type ProjectInput, type ProjectOverview, type WorkspaceLink } from '@/api/projects'

export const useProjectStore = defineStore('projects', () => {
  const projects = ref<Project[]>([])
  const loading = ref(false)
  const loaded = ref(false)
  const error = ref<string | null>(null)
  const overviews = ref(new Map<number, ProjectOverview>())
  const workspaceLinks = ref(new Map<number, WorkspaceLink>())

  // Only a fetch before the first successful load shows loading, and an
  // error stays until a fetch succeeds, so background polls don't flicker
  // the page between its states.
  async function fetchProjects() {
    const initial = !loaded.value
    if (initial && !error.value) loading.value = true
    try {
      const response = await projectApi.list()
      projects.value = response.projects ?? []
      loaded.value = true
      error.value = null
    } catch (e) {
      if (initial) error.value = 'Failed to load projects'
      throw e
    } finally {
      loading.value = false
    }
  }

  // Activity counts per project, and which project and task or merge request
  // each project-linked workspace belongs to.
  async function fetchOverview() {
    const response = await projectApi.overview()
    overviews.value = new Map((response.projects ?? []).map((o) => [o.projectId, o]))
    workspaceLinks.value = new Map((response.workspaces ?? []).map((l) => [l.workspaceId, l]))
  }

  async function createProject(input: ProjectInput) {
    const response = await projectApi.create(input)
    projects.value.unshift(response.project)
    return response.project
  }

  // Saves the editable fields, then the upstream if the input names a
  // different one. The fields stay saved when the upstream is refused, so
  // the project as saved is returned with the reason.
  async function updateProject(id: number, input: ProjectInput): Promise<{ project: Project; upstreamError?: string }> {
    let { project } = await projectApi.update(id, input)
    let upstreamError: string | undefined
    const upstreamUrl = input.upstreamUrl?.trim()
    if (upstreamUrl !== undefined && upstreamUrl !== project.upstreamUrl) {
      try {
        project = (await projectApi.setUpstream(id, upstreamUrl)).project
      } catch (e) {
        upstreamError = e instanceof Error ? e.message : 'Failed to change the upstream'
      }
    }
    const index = projects.value.findIndex((p) => p.id === id)
    if (index !== -1) {
      projects.value[index] = project
    }
    return { project, upstreamError }
  }

  async function deleteProject(id: number) {
    await projectApi.delete(id)
    projects.value = projects.value.filter((p) => p.id !== id)
  }

  return {
    projects,
    loading,
    loaded,
    error,
    overviews,
    workspaceLinks,
    fetchProjects,
    fetchOverview,
    createProject,
    updateProject,
    deleteProject,
  }
})
