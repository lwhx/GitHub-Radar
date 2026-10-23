import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError, apiFetch } from './client'

function fakeResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: () => Promise.resolve(body === undefined ? '' : JSON.stringify(body)),
  } as unknown as Response
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('apiFetch', () => {
  it('returns the data envelope on success', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(fakeResponse(200, { data: { repositories: 3 } })),
    )
    await expect(apiFetch('/api/v1/stats')).resolves.toEqual({ repositories: 3 })
  })

  it('sends JSON body and CSRF header when provided', async () => {
    const spy = vi.fn().mockResolvedValue(fakeResponse(200, { data: null }))
    vi.stubGlobal('fetch', spy)

    await apiFetch('/api/v1/admin/auth/login', {
      body: { username: 'ops', password: 'secret' },
      csrfToken: 'token-1',
    })

    const [url, init] = spy.mock.calls[0] as unknown as [string, { headers: Record<string, string>; method: string }]
    expect(url).toBe('/api/v1/admin/auth/login')
    expect(init.method).toBe('POST')
    expect(init.headers['X-CSRF-Token']).toBe('token-1')
    expect(init.headers['Content-Type']).toBe('application/json')
  })

  it('throws ApiError with code, message and request id on error responses', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        fakeResponse(429, {
          error: { code: 'RATE_LIMITED', message: 'Too many login attempts' },
          request_id: 'req-1',
        }),
      ),
    )

    const failure = await apiFetch('/api/v1/admin/auth/login').catch((error: unknown) => error)
    expect(failure).toBeInstanceOf(ApiError)
    const apiError = failure as ApiError
    expect(apiError.status).toBe(429)
    expect(apiError.code).toBe('RATE_LIMITED')
    expect(apiError.message).toBe('Too many login attempts')
    expect(apiError.requestId).toBe('req-1')
  })

  it('returns undefined for 204 No Content responses', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(fakeResponse(204, undefined)))
    await expect(apiFetch('/api/v1/admin/auth/logout', { method: 'POST' })).resolves.toBeUndefined()
  })

  it('throws a network ApiError when fetch itself rejects', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('connection reset')))

    const failure = await apiFetch('/healthz').catch((error: unknown) => error)
    expect(failure).toBeInstanceOf(ApiError)
    expect((failure as ApiError).code).toBe('NETWORK_ERROR')
    expect((failure as ApiError).status).toBe(0)
  })
})
