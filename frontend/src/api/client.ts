const BASE_URL = ''

export class ApiError extends Error {
  status: number
  body: unknown

  constructor(message: string, status: number, body: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }
}

class ApiClient {
  private baseUrl: string

  constructor(baseUrl: string) {
    this.baseUrl = baseUrl
  }

  async get<T>(path: string): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`)
    if (!res.ok) {
      throw await this.errorFromResponse(res, 'GET', path)
    }
    return res.json() as Promise<T>
  }

  async post<T>(path: string, body: unknown): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!res.ok) {
      throw await this.errorFromResponse(res, 'POST', path)
    }
    if (res.status === 204) return undefined as T
    return res.json() as Promise<T>
  }

  async postForm<T>(path: string, body: FormData): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method: 'POST',
      body,
    })
    if (!res.ok) {
      throw await this.errorFromResponse(res, 'POST', path)
    }
    if (res.status === 204) return undefined as T
    return res.json() as Promise<T>
  }

  async put<T>(path: string, body: unknown): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!res.ok) {
      throw await this.errorFromResponse(res, 'PUT', path)
    }
    if (res.status === 204) return undefined as T
    return res.json() as Promise<T>
  }

  async delete(path: string): Promise<void> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method: 'DELETE',
    })
    if (!res.ok) {
      throw await this.errorFromResponse(res, 'DELETE', path)
    }
  }

  private async errorFromResponse(res: Response, method: string, path: string): Promise<ApiError> {
    let body: unknown = null
    try {
      body = await res.json()
    } catch {
      body = null
    }

    const message = isErrorBody(body) && body.error
      ? body.error
      : `${method} ${path} failed: ${res.status}`
    return new ApiError(message, res.status, body)
  }
}

function isErrorBody(body: unknown): body is { error: string } {
  return typeof body === 'object' && body !== null && 'error' in body && typeof body.error === 'string'
}

export const apiClient = new ApiClient(BASE_URL)
