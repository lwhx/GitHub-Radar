import { reactive } from 'vue'

import { ApiError, apiFetch } from '../api/client'
import type { AdminUser } from '../api/types'

interface LoginResponse {
  admin: AdminUser
  csrf_token: string
}

interface SessionResponse {
  admin: AdminUser
}

export const authState = reactive<{ user: AdminUser | null; loaded: boolean }>({
  user: null,
  loaded: false,
})

let csrfToken: string | null = null

export async function fetchSession(): Promise<boolean> {
  try {
    const data = await apiFetch<SessionResponse>('/api/v1/admin/auth/session')
    authState.user = data.admin
    return true
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      authState.user = null
      return false
    }
    throw error
  } finally {
    authState.loaded = true
  }
}

export async function ensureSession(): Promise<boolean> {
  if (!authState.loaded) return fetchSession()
  return authState.user !== null
}

export async function login(username: string, password: string): Promise<void> {
  const data = await apiFetch<LoginResponse>('/api/v1/admin/auth/login', {
    body: { username, password },
  })
  authState.user = data.admin
  authState.loaded = true
  csrfToken = data.csrf_token
}

export async function logout(): Promise<void> {
  try {
    await apiFetch<null>('/api/v1/admin/auth/logout', {
      method: 'POST',
      csrfToken: csrfToken ?? undefined,
    })
  } finally {
    authState.user = null
    csrfToken = null
  }
}

export function csrfTokenForRequest(): string | undefined {
  if (csrfToken) return csrfToken
  if (typeof document === 'undefined') return undefined
  const csrfCookie = document.cookie
    .split(';')
    .map((part) => part.trim())
    .find((part) => part.startsWith('radar_csrf='))
  if (!csrfCookie) return undefined
  const value = csrfCookie.slice('radar_csrf='.length)
  return value ? decodeURIComponent(value) : undefined
}

export function resetAuthForTests(): void {
  authState.user = null
  authState.loaded = false
  csrfToken = null
}
