import { defineStore } from 'pinia'
import { ref } from 'vue'
import { projectApi, type Project, type ProjectInput, type ProjectOverview, type WorkspaceLink } from '@/api/projects'

export const useProjectStore = defineStore('projects', () => {
  const projects = ref<Project[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  const overviews = ref(new Map<number, ProjectOverview>())
  const workspaceLinks = ref(new Map<number, WorkspaceLink>())

  async function fetchProjects() {
    loading.value = true
    error.value = null
    try {
      const response = await projectApi.list()
      projects.value = response.projects ?? []
    } catch (e) {
      error.value = 'Failed to load projects'
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

  async function updateProject(id: number, input: ProjectInput) {
    const response = await projectApi.update(id, input)
    const index = projects.value.findIndex((p) => p.id === id)
    if (index !== -1) {
      projects.value[index] = response.project
    }
    return response.project
  }

  async function deleteProject(id: number) {
    await projectApi.delete(id)
    projects.value = projects.value.filter((p) => p.id !== id)
  }

  return {
    projects,
    loading,
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
