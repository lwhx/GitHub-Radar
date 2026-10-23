export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId: string | null

  constructor(status: number, code: string, message: string, requestId: string | null) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.requestId = requestId
  }
}

export interface ApiFetchOptions {
  method?: 'GET' | 'POST' | 'PATCH'
  body?: unknown
  csrfToken?: string
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function parseErrorPayload(payload: unknown): { code: string; message: string; requestId: string | null } {
  let code = 'INTERNAL_ERROR'
  let message = '服务暂时不可用，请稍后重试'
  let requestId: string | null = null
  if (isRecord(payload)) {
    if (isRecord(payload.error)) {
      if (typeof payload.error.code === 'string') code = payload.error.code
      if (typeof payload.error.message === 'string') message = payload.error.message
    }
    if (typeof payload.request_id === 'string') requestId = payload.request_id
  }
  return { code, message, requestId }
}

export async function apiFetch<T>(path: string, options: ApiFetchOptions = {}): Promise<T> {
  const headers: Record<string, string> = {}
  if (options.body !== undefined) headers['Content-Type'] = 'application/json'
  if (options.csrfToken) headers['X-CSRF-Token'] = options.csrfToken

  let response: Response
  try {
    response = await fetch(path, {
      method: options.method ?? (options.body !== undefined ? 'POST' : 'GET'),
      headers,
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
      credentials: 'same-origin',
    })
  } catch {
    throw new ApiError(0, 'NETWORK_ERROR', '网络请求失败，请检查网络后重试', null)
  }

  if (response.status === 204) return undefined as T

  const text = await response.text()
  let payload: unknown = null
  if (text.length > 0) {
    try {
      payload = JSON.parse(text)
    } catch {
      payload = null
    }
  }

  if (!response.ok || !isRecord(payload) || !('data' in payload)) {
    const parsed = parseErrorPayload(payload)
    throw new ApiError(response.status, parsed.code, parsed.message, parsed.requestId)
  }
  return payload.data as T
}
