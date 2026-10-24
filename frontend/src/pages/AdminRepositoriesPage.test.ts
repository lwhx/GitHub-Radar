import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import AdminRepositoriesPage from './AdminRepositoriesPage.vue'

function fakeResponse(status: number, body: unknown): Response {
  return { ok: status >= 200 && status < 300, status, text: () => Promise.resolve(JSON.stringify(body)) } as unknown as Response
}

function repository(overrides: Record<string, unknown> = {}) {
  return {
    id: 9,
    github_id: 99001,
    full_name: 'acme/radar',
    html_url: 'https://github.com/acme/radar',
    description: 'managed repository',
    language: 'Go',
    stars_count: 88,
    hidden: true,
    sync_status: 'synced',
    last_synced_at: '2026-10-24T09:00:00Z',
    category_ids: [71],
    ...overrides,
  }
}

const categoriesResponse = {
  data: {
    items: [
      { id: 71, slug: 'managed', title: '托管项目', description: null, sort_order: 1, enabled: true, repo_count: 1 },
      { id: 72, slug: 'ai', title: 'AI', description: null, sort_order: 2, enabled: true, repo_count: 0 },
    ],
  },
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolver) => {
    resolve = resolver
  })
  return { promise, resolve }
}

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'radar_csrf=; Max-Age=0; Path=/'
})

describe('AdminRepositoriesPage', () => {
  it('lists hidden repositories and all persisted synchronization states', async () => {
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(
        fakeResponse(200, {
          data: {
            items: [
              repository(),
              repository({ id: 10, full_name: 'acme/retrying', sync_status: 'retrying' }),
              repository({ id: 11, full_name: 'acme/inaccessible', sync_status: 'inaccessible' }),
              repository({ id: 12, full_name: 'acme/unknown', sync_status: 'future-state' }),
            ],
            total: 4,
            page: 1,
            page_size: 20,
          },
        }),
      )
      .mockResolvedValueOnce(fakeResponse(200, categoriesResponse))
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminRepositoriesPage)
    await flushPromises()

    expect(wrapper.text()).toContain('acme/radar')
    expect(wrapper.text()).toContain('已隐藏')
    expect(wrapper.text()).toContain('88')
    expect(wrapper.text()).toContain('托管项目')
    expect(wrapper.text()).toContain('待重试')
    expect(wrapper.text()).toContain('不可访问')
    expect(wrapper.text()).toContain('未知状态')
  })

  it('imports a real GitHub URL and reloads the authoritative first page', async () => {
    document.cookie = 'radar_csrf=csrf-import; Path=/'
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { items: [], total: 0, page: 1, page_size: 20 } }))
      .mockResolvedValueOnce(fakeResponse(200, categoriesResponse))
      .mockResolvedValueOnce(fakeResponse(201, { data: { repository: repository({ hidden: false }), created: true } }))
      .mockResolvedValueOnce(
        fakeResponse(200, { data: { items: [repository({ hidden: false })], total: 1, page: 1, page_size: 20 } }),
      )
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminRepositoriesPage)
    await flushPromises()
    await wrapper.get('[data-test="import-url"]').setValue('https://github.com/acme/radar')
    await wrapper.get('[data-test="import-form"]').trigger('submit')
    await flushPromises()

    const [url, init] = fetchSpy.mock.calls[2] as unknown as [string, { method: string; headers: Record<string, string>; body: string }]
    expect(url).toBe('/api/v1/admin/repos/import')
    expect(init.method).toBe('POST')
    expect(init.headers['X-CSRF-Token']).toBe('csrf-import')
    expect(JSON.parse(init.body)).toEqual({ url: 'https://github.com/acme/radar' })
    expect(fetchSpy.mock.calls[3]?.[0]).toBe('/api/v1/admin/repos?page=1&page_size=20')
    expect(wrapper.find('[data-test="repo-row-9"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('已导入 acme/radar')
  })

  it('ignores a stale list response that completes after the import reload', async () => {
    document.cookie = 'radar_csrf=csrf-race; Path=/'
    const initialList = deferred<Response>()
    const imported = repository({ id: 40, github_id: 40, full_name: 'acme/imported', hidden: false })
    let listCalls = 0
    const fetchSpy = vi.fn((input: string | URL | Request) => {
      const target = String(input)
      if (target === '/api/v1/admin/categories') return Promise.resolve(fakeResponse(200, categoriesResponse))
      if (target === '/api/v1/admin/repos/import') {
        return Promise.resolve(fakeResponse(201, { data: { repository: imported, created: true } }))
      }
      listCalls += 1
      if (listCalls === 1) return initialList.promise
      return Promise.resolve(fakeResponse(200, { data: { items: [imported], total: 1, page: 1, page_size: 20 } }))
    })
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminRepositoriesPage)
    await flushPromises()
    await wrapper.get('[data-test="import-url"]').setValue('https://github.com/acme/imported')
    await wrapper.get('[data-test="import-form"]').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('acme/imported')

    initialList.resolve(fakeResponse(200, { data: { items: [repository({ full_name: 'acme/stale' })], total: 1, page: 1, page_size: 20 } }))
    await flushPromises()

    expect(wrapper.text()).toContain('acme/imported')
    expect(wrapper.text()).not.toContain('acme/stale')
  })

  it('reloads rather than duplicating a refreshed repository', async () => {
    document.cookie = 'radar_csrf=csrf-refresh; Path=/'
    const refreshed = repository({ hidden: false, stars_count: 99 })
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { items: [repository()], total: 1, page: 1, page_size: 20 } }))
      .mockResolvedValueOnce(fakeResponse(200, categoriesResponse))
      .mockResolvedValueOnce(fakeResponse(200, { data: { repository: refreshed, created: false } }))
      .mockResolvedValueOnce(fakeResponse(200, { data: { items: [refreshed], total: 1, page: 1, page_size: 20 } }))
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminRepositoriesPage)
    await flushPromises()
    await wrapper.get('[data-test="import-url"]').setValue('https://github.com/acme/radar')
    await wrapper.get('[data-test="import-form"]').trigger('submit')
    await flushPromises()

    expect(wrapper.findAll('[data-test="repo-row-9"]')).toHaveLength(1)
    expect(wrapper.get('[data-test="repo-row-9"]').text()).toContain('99')
    expect(wrapper.text()).toContain('已刷新 acme/radar')
  })

  it('returns to page one after creating a repository from a later page', async () => {
    document.cookie = 'radar_csrf=csrf-later-page; Path=/'
    const oldRepository = repository({ id: 3, github_id: 3, full_name: 'acme/old' })
    const createdRepository = repository({ id: 30, github_id: 30, full_name: 'acme/new', hidden: false })
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { items: [repository()], total: 21, page: 1, page_size: 20 } }))
      .mockResolvedValueOnce(fakeResponse(200, categoriesResponse))
      .mockResolvedValueOnce(fakeResponse(200, { data: { items: [oldRepository], total: 21, page: 2, page_size: 20 } }))
      .mockResolvedValueOnce(fakeResponse(201, { data: { repository: createdRepository, created: true } }))
      .mockResolvedValueOnce(fakeResponse(200, { data: { items: [createdRepository], total: 22, page: 1, page_size: 20 } }))
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminRepositoriesPage)
    await flushPromises()
    await wrapper.get('[data-test="next-page"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('acme/old')

    await wrapper.get('[data-test="import-url"]').setValue('https://github.com/acme/new')
    await wrapper.get('[data-test="import-form"]').trigger('submit')
    await flushPromises()

    expect(fetchSpy.mock.calls[4]?.[0]).toBe('/api/v1/admin/repos?page=1&page_size=20')
    expect(wrapper.find('[data-test="repo-row-30"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('第 1 / 2 页')
  })

  it('updates visibility, categories and queues a resync from the modal', async () => {
    document.cookie = 'radar_csrf=csrf-update; Path=/'
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { items: [repository()], total: 1, page: 1, page_size: 20 } }))
      .mockResolvedValueOnce(fakeResponse(200, categoriesResponse))
      .mockResolvedValueOnce(
        fakeResponse(200, { data: repository({ hidden: false, category_ids: [71, 72], sync_status: 'pending' }) }),
      )
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminRepositoriesPage)
    await flushPromises()
    await wrapper.get('[data-test="manage-repo-9"]').trigger('click')
    expect(wrapper.get('[data-test="repo-resync"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('隐藏仓库不会进入同步队列')
    await wrapper.get('[data-test="repo-hidden"]').setValue(false)
    await wrapper.get('[data-test="repo-category-72"]').setValue(true)
    await wrapper.get('[data-test="repo-resync"]').setValue(true)
    await wrapper.get('[data-test="repository-edit-form"]').trigger('submit')
    await flushPromises()

    const [url, init] = fetchSpy.mock.calls[2] as unknown as [string, { method: string; body: string }]
    expect(url).toBe('/api/v1/admin/repos/9')
    expect(init.method).toBe('PATCH')
    expect(JSON.parse(init.body)).toEqual({ hidden: false, category_ids: [71, 72], resync: true })
    expect(wrapper.text()).toContain('已更新 acme/radar')
    expect(wrapper.find('[data-test="repository-edit-modal"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="repo-row-9"]').text()).toContain('公开')
    expect(wrapper.get('[data-test="repo-row-9"]').text()).toContain('待同步')
  })

  it('searches through the management endpoint with encoded input', async () => {
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { items: [], total: 0, page: 1, page_size: 20 } }))
      .mockResolvedValueOnce(fakeResponse(200, categoriesResponse))
      .mockResolvedValueOnce(fakeResponse(200, { data: { items: [repository()], total: 1, page: 1, page_size: 20 } }))
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminRepositoriesPage)
    await flushPromises()
    await wrapper.get('[data-test="repository-query"]').setValue('acme/radar %')
    await wrapper.get('[data-test="repository-search-form"]').trigger('submit')
    await flushPromises()

    expect(fetchSpy.mock.calls[2]?.[0]).toBe('/api/v1/admin/repos?q=acme%2Fradar+%25&page=1&page_size=20')
  })
})
