import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import AdminOperationsPage from './AdminOperationsPage.vue'

function response(data: unknown, status = 200): Response {
  return { ok: status >= 200 && status < 300, status, text: () => Promise.resolve(JSON.stringify({ data })) } as Response
}

const settings = {
  site_name: 'GitHub Radar', site_description: '中文开源发现', timezone: 'Asia/Shanghai', default_page_size: 20,
  ai_enabled: true, ai_base_url: 'https://api.example.com/v1', ai_model: 'glm-4', has_ai_api_key: true,
  trusted_proxies: ['127.0.0.1'],
}
const backup = {
  name: 'radar-daily-20261024T040000.000000000Z.db', kind: 'daily', created_at: '2026-10-24T04:00:00Z',
  size_bytes: 1048576, sha256: 'a'.repeat(64), verified: false, download_url: '/api/v1/admin/backups/radar.db',
}
const audit = { id: 1, admin_id: 1, action: 'admin.settings.update', target: 'site', result: 'success', created_at: '2026-10-24T05:00:00Z', request_id: 'req-1' }

function installFetch() {
  const calls: Array<[string, RequestInit]> = []
  const spy = vi.fn(async (input: string | URL | Request, init: RequestInit = {}) => {
    const url = String(input)
    calls.push([url, init])
    if (url === '/api/v1/admin/settings' && init.method === 'PATCH') return response(settings)
    if (url === '/api/v1/admin/settings') return response(settings)
    if (url === '/api/v1/admin/backups' && init.method === 'POST') {
      return response({ ...backup, name: 'radar-manual-20261024T060000.000000000Z.db', kind: 'manual' }, 201)
    }
    if (url === '/api/v1/admin/backups') return response({ items: [backup], retention: { daily: 7, weekly: 4 } })
    if (url.includes('/verify')) return response({ name: backup.name, verified: true })
    if (url.startsWith('/api/v1/admin/audit-logs')) return response({ items: [audit] })
    throw new Error(`unexpected URL ${url}`)
  })
  vi.stubGlobal('fetch', spy)
  return { spy, calls }
}

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'radar_csrf=; Max-Age=0; Path=/'
})

describe('AdminOperationsPage', () => {
  it('loads settings, backups and audit logs without exposing the secret', async () => {
    installFetch()
    const wrapper = mount(AdminOperationsPage)
    await flushPromises()

    expect((wrapper.get('[data-test="site-name"]').element as HTMLInputElement).value).toBe('GitHub Radar')
    expect(wrapper.text()).toContain('已保存密钥（不会回传）')
    expect(wrapper.text()).toContain(backup.name)
    expect(wrapper.text()).toContain('admin.settings.update')
    expect(wrapper.find('[data-test="ai-key"]').exists()).toBe(false)
  })

  it('saves settings with CSRF and preserves the existing key by omission', async () => {
    document.cookie = 'radar_csrf=csrf-ops; Path=/'
    const { calls } = installFetch()
    const wrapper = mount(AdminOperationsPage)
    await flushPromises()
    await wrapper.get('[data-test="site-name"]').setValue('雷达站')
    await wrapper.get('[data-test="settings-form"]').trigger('submit')
    await flushPromises()

    const call = calls.find(([url, init]) => url === '/api/v1/admin/settings' && init.method === 'PATCH')
    expect(call).toBeTruthy()
    const init = call?.[1] as { headers: Record<string, string>; body: string }
    expect(init.headers['X-CSRF-Token']).toBe('csrf-ops')
    expect(JSON.parse(init.body)).not.toHaveProperty('ai_api_key')
    expect(wrapper.text()).toContain('设置已保存')
  })

  it('creates and verifies backups and filters audit actions', async () => {
    document.cookie = 'radar_csrf=csrf-backup; Path=/'
    const { calls } = installFetch()
    const wrapper = mount(AdminOperationsPage)
    await flushPromises()

    await wrapper.get('[data-test="create-backup"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('radar-manual-20261024T060000.000000000Z.db')
    const create = calls.find(([url, init]) => url === '/api/v1/admin/backups' && init.method === 'POST')
    expect((create?.[1].headers as Record<string, string>)['X-CSRF-Token']).toBe('csrf-backup')

    const verifyButton = wrapper.findAll('button').find((button) => button.text() === '校验')
    await verifyButton?.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('完整性正常')

    await wrapper.get('[data-test="audit-filter"]').setValue('admin.backup.create')
    await wrapper.get('[data-test="audit-filter"]').element.closest('form')?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flushPromises()
    expect(calls.some(([url]) => url.includes('action=admin.backup.create'))).toBe(true)
  })
})
