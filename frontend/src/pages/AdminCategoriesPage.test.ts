import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import AdminCategoriesPage from './AdminCategoriesPage.vue'

function fakeResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response
}

function category(overrides: Record<string, unknown> = {}) {
  return {
    id: 1,
    slug: 'ai',
    title: 'AI 与大模型',
    description: '模型生态',
    sort_order: 1,
    enabled: true,
    repo_count: 12,
    ...overrides,
  }
}

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'radar_csrf=; Max-Age=0; Path=/'
})

describe('AdminCategoriesPage', () => {
  it('lists categories including disabled ones with visible repo counts', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        fakeResponse(200, {
          data: { items: [category(), category({ id: 2, slug: 'legacy', title: '已停用分类', enabled: false, repo_count: 0 })] },
        }),
      ),
    )

    const wrapper = mount(AdminCategoriesPage)
    await flushPromises()

    expect(wrapper.text()).toContain('AI 与大模型')
    expect(wrapper.text()).toContain('已停用分类')
    expect(wrapper.text()).toContain('已停用')
    expect(wrapper.text()).toContain('12')
  })

  it('creates a category and prepends it to the list', async () => {
    document.cookie = 'radar_csrf=csrf-create; Path=/'
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { items: [category()] } }))
      .mockResolvedValueOnce(
        fakeResponse(201, { data: category({ id: 9, slug: 'ai-agents', title: 'AI Agent', repo_count: 0 }) }),
      )
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminCategoriesPage)
    await flushPromises()
    await wrapper.get('[data-test="category-slug"]').setValue('ai-agents')
    await wrapper.get('[data-test="category-title"]').setValue('AI Agent')
    await wrapper.get('[data-test="category-form"]').trigger('submit')
    await flushPromises()

    const [url, init] = fetchSpy.mock.calls[1] as unknown as [
      string,
      { method: string; headers: Record<string, string>; body: string },
    ]
    expect(url).toBe('/api/v1/admin/categories')
    expect(init.method).toBe('POST')
    expect(init.headers['X-CSRF-Token']).toBe('csrf-create')
    expect(init.body).toContain('ai-agents')
    expect(wrapper.text()).toContain('已创建分类“AI Agent”')
    expect(wrapper.findAll('[data-test^="category-row-"]')).toHaveLength(2)
  })

  it('toggles a category and reports the new state', async () => {
    document.cookie = 'radar_csrf=csrf-toggle; Path=/'
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { items: [category()] } }))
      .mockResolvedValueOnce(fakeResponse(200, { data: category({ enabled: false }) }))
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminCategoriesPage)
    await flushPromises()
    await wrapper.get('[data-test="toggle-category-1"]').trigger('click')
    await flushPromises()

    const [url, init] = fetchSpy.mock.calls[1] as unknown as [string, { method: string; body: string }]
    expect(url).toBe('/api/v1/admin/categories/1')
    expect(init.method).toBe('PATCH')
    expect(init.body).toContain('"enabled":false')
    expect(wrapper.text()).toContain('已停用分类“AI 与大模型”')
  })

  it('deletes a category after confirmation', async () => {
    document.cookie = 'radar_csrf=csrf-delete; Path=/'
    const confirmSpy = vi.fn().mockReturnValue(true)
    vi.stubGlobal('confirm', confirmSpy)
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { items: [category()] } }))
      .mockResolvedValueOnce(fakeResponse(200, { data: { id: 1, deleted: true } }))
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminCategoriesPage)
    await flushPromises()
    await wrapper.get('[data-test="delete-category-1"]').trigger('click')
    await flushPromises()

    expect(confirmSpy).toHaveBeenCalledTimes(1)
    const [, init] = fetchSpy.mock.calls[1] as unknown as [string, { method: string }]
    expect(init.method).toBe('DELETE')
    expect(wrapper.find('[data-test="category-row-1"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('已删除分类“AI 与大模型”')
  })

  it('surfaces backend validation errors without dropping the row', async () => {
    document.cookie = 'radar_csrf=csrf-error; Path=/'
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { items: [] } }))
      .mockResolvedValueOnce(
        fakeResponse(400, {
          error: { code: 'INVALID_CATEGORY', message: 'slug must be 2-64 lowercase letters, digits or hyphens' },
        }),
      )
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminCategoriesPage)
    await flushPromises()
    await wrapper.get('[data-test="category-slug"]').setValue('Bad Slug')
    await wrapper.get('[data-test="category-title"]').setValue('X')
    await wrapper.get('[data-test="category-form"]').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('slug must be 2-64 lowercase letters')
  })
})
