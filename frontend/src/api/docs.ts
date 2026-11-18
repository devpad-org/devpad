import { apiClient } from './client'

export interface DocSection {
  id: string
  title: string
  level: number
}

export interface DocPageInfo {
  slug: string
  title: string
  summary: string
  admin: boolean
  sections: DocSection[]
}

export interface DocPage extends DocPageInfo {
  body: string
}

export const docsApi = {
  list() {
    return apiClient.get<{ pages: DocPageInfo[] }>('/api/docs')
  },
  get(slug: string) {
    return apiClient.get<DocPage>(`/api/docs/${encodeURIComponent(slug)}`)
  },
}
