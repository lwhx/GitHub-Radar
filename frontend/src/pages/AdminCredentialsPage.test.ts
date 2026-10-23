import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import AdminCredentialsPage from './AdminCredentialsPage.vue'

function fakeResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response
}

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'radar_csrf=; Max-Age=0; Path=/'
})

describe('AdminCredentialsPage', () => {
  it('loads masked credentials without rendering a token secret', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        fakeResponse(200, {
          data: {
            credentials: [
              {
                id: 1,
                label: '主账号',
                auth_identity: '42',
                auth_login: 'octocat',
                token_hint: '…1234',
                enabled: true,
                status: 'active',
                priority: 100,
                last_checked_at: '2026-10-23T12:00:00Z',
                created_at: '2026-10-23T12:00:00Z',
                updated_at: '2026-10-23T12:00:00Z',
                rate_limits: [
                  {
                    resource: 'search',
                    limit: 30,
                    used: 2,
                    remaining: 28,
                    reset_at: '2026-10-23T13:00:00Z',
                    cooldown_until: null,
                  },
                ],
              },
            ],
          },
        }),
      ),
    )

    const wrapper = mount(AdminCredentialsPage)
    await flushPromises()

    expect(wrapper.text()).toContain('主账号')
    expect(wrapper.text()).toContain('@octocat')
    expect(wrapper.text()).toContain('…1234')
    expect(wrapper.text()).toContain('28 / 30')
    expect(wrapper.text()).not.toContain('github_pat_secret')
  })

  it('creates a credential with the CSRF token and clears the token input', async () => {
    document.cookie = 'radar_csrf=csrf-1; Path=/'
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { credentials: [] } }))
      .mockResolvedValueOnce(
        fakeResponse(201, {
          data: {
            id: 2,
            label: '备用账号',
            auth_identity: '84',
            auth_login: 'hubot',
            token_hint: '…9999',
            enabled: true,
            status: 'active',
            priority: 20,
            last_checked_at: '2026-10-23T12:00:00Z',
            created_at: '2026-10-23T12:00:00Z',
            updated_at: '2026-10-23T12:00:00Z',
          },
        }),
      )
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminCredentialsPage)
    await flushPromises()
    await wrapper.find('#credential-label').setValue('备用账号')
    await wrapper.find('#credential-token').setValue('dummy-token-for-test-9999')
    await wrapper.find('#credential-priority').setValue('20')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    const [, init] = fetchSpy.mock.calls[1] as unknown as [string, { method: string; headers: Record<string, string>; body: string }]
    expect(init.method).toBe('POST')
    expect(init.headers['X-CSRF-Token']).toBe('csrf-1')
    expect(JSON.parse(init.body)).toEqual({ label: '备用账号', token: 'dummy-token-for-test-9999', priority: 20 })
    expect((wrapper.find('#credential-token').element as HTMLInputElement).value).toBe('')
    expect(wrapper.text()).toContain('备用账号')
    expect(wrapper.text()).not.toContain('dummy-token-for-test-9999')
  })

  it('changes the enabled state with PATCH and keeps the secret out of the request', async () => {
    document.cookie = 'radar_csrf=csrf-toggle; Path=/'
    const credential = {
      id: 7,
      label: '主账号',
      auth_identity: '42',
      auth_login: 'octocat',
      token_hint: '…1234',
      enabled: true,
      status: 'active',
      priority: 100,
      last_checked_at: null,
      created_at: '2026-10-23T12:00:00Z',
      updated_at: '2026-10-23T12:00:00Z',
      rate_limits: [],
    }
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { credentials: [credential] } }))
      .mockResolvedValueOnce(fakeResponse(200, { data: { id: 7, enabled: false } }))
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminCredentialsPage)
    await flushPromises()
    await wrapper.get('[data-test="toggle-credential-7"]').trigger('click')
    await flushPromises()

    const [url, init] = fetchSpy.mock.calls[1] as unknown as [string, { method: string; headers: Record<string, string>; body: string }]
    expect(url).toBe('/api/v1/admin/credentials/7')
    expect(init.method).toBe('PATCH')
    expect(init.headers['X-CSRF-Token']).toBe('csrf-toggle')
    expect(JSON.parse(init.body)).toEqual({ enabled: false })
    expect(wrapper.text()).toContain('已停用')
    expect(init.body).not.toContain('1234')
  })

  it('re-checks a credential and shows the refreshed identity', async () => {
    document.cookie = 'radar_csrf=csrf-test; Path=/'
    const credential = {
      id: 7,
      label: '主账号',
      auth_identity: '42',
      auth_login: 'octocat',
      token_hint: '…1234',
      enabled: true,
      status: 'active',
      priority: 100,
      last_checked_at: null,
      created_at: '2026-10-23T12:00:00Z',
      updated_at: '2026-10-23T12:00:00Z',
      rate_limits: [],
    }
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { credentials: [credential] } }))
      .mockResolvedValueOnce(fakeResponse(200, { data: { credential: { ...credential, auth_login: 'hubot' } } }))
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminCredentialsPage)
    await flushPromises()
    await wrapper.get('[data-test="test-credential-7"]').trigger('click')
    await flushPromises()

    const [url, init] = fetchSpy.mock.calls[1] as unknown as [string, { method: string; headers: Record<string, string> }]
    expect(url).toBe('/api/v1/admin/credentials/7/test')
    expect(init.method).toBe('POST')
    expect(init.headers['X-CSRF-Token']).toBe('csrf-test')
    expect(wrapper.text()).toContain('验证通过')
    expect(wrapper.text()).toContain('@hubot')
  })

  it('deletes a credential after confirmation and drops the row', async () => {
    document.cookie = 'radar_csrf=csrf-delete; Path=/'
    const confirmSpy = vi.fn().mockReturnValue(true)
    vi.stubGlobal('confirm', confirmSpy)
    const credential = {
      id: 7,
      label: '主账号',
      auth_identity: '42',
      auth_login: 'octocat',
      token_hint: '…1234',
      enabled: true,
      status: 'active',
      priority: 100,
      last_checked_at: null,
      created_at: '2026-10-23T12:00:00Z',
      updated_at: '2026-10-23T12:00:00Z',
      rate_limits: [],
    }
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { credentials: [credential] } }))
      .mockResolvedValueOnce(fakeResponse(200, { data: { id: 7, deleted: true } }))
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminCredentialsPage)
    await flushPromises()
    await wrapper.get('[data-test="delete-credential-7"]').trigger('click')
    await flushPromises()

    expect(confirmSpy).toHaveBeenCalledTimes(1)
    const [url, init] = fetchSpy.mock.calls[1] as unknown as [string, { method: string; headers: Record<string, string> }]
    expect(url).toBe('/api/v1/admin/credentials/7')
    expect(init.method).toBe('DELETE')
    expect(wrapper.find('[data-test="delete-credential-7"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('已删除')
  })

  it('does not delete when the operator cancels the confirmation', async () => {
    document.cookie = 'radar_csrf=csrf-cancel; Path=/'
    vi.stubGlobal('confirm', vi.fn().mockReturnValue(false))
    const fetchSpy = vi.fn().mockResolvedValueOnce(
      fakeResponse(200, {
        data: {
          credentials: [
            {
              id: 9,
              label: '保留账号',
              auth_identity: '42',
              auth_login: 'octocat',
              token_hint: '…1234',
              enabled: true,
              status: 'active',
              priority: 0,
              last_checked_at: null,
              created_at: '2026-10-23T12:00:00Z',
              updated_at: '2026-10-23T12:00:00Z',
              rate_limits: [],
            },
          ],
        },
      }),
    )
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminCredentialsPage)
    await flushPromises()
    await wrapper.get('[data-test="delete-credential-9"]').trigger('click')
    await flushPromises()

    expect(fetchSpy).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('保留账号')
  })
})
