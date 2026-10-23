import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import router from '../router'
import CategoriesPage from './CategoriesPage.vue'

function fakeResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('CategoriesPage', () => {
  it('renders categories with counts and links to the filtered list', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        fakeResponse(200, {
          data: {
            items: [
              { slug: 'ai', title: 'AI 与大模型', description: '大模型相关项目', repo_count: 12 },
              { slug: 'devops', title: 'DevOps 工具', description: null, repo_count: 0 },
            ],
          },
        }),
      ),
    )

    await router.push('/categories')
    await router.isReady()
    const wrapper = mount(CategoriesPage, { global: { plugins: [router] } })
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('AI 与大模型')
    expect(text).toContain('12')
    expect(text).toContain('大模型相关项目')
    const link = wrapper.findAll('a').find((a) => a.attributes('href')?.includes('/repos?category=ai'))
    expect(link).toBeTruthy()
  })

  it('shows the honest empty state when no categories exist', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(fakeResponse(200, { data: { items: [] } })))

    await router.push('/categories')
    await router.isReady()
    const wrapper = mount(CategoriesPage, { global: { plugins: [router] } })
    await flushPromises()

    expect(wrapper.text()).toContain('暂无分类')
    expect(wrapper.text()).toContain('不使用演示分类')
  })

  it('shows an error alert with retry when the request fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(fakeResponse(500, { error: { code: 'INTERNAL_ERROR', message: 'Unable to list categories' } })),
    )

    await router.push('/categories')
    await router.isReady()
    const wrapper = mount(CategoriesPage, { global: { plugins: [router] } })
    await flushPromises()

    const alert = wrapper.find('[role="alert"]')
    expect(alert.exists()).toBe(true)
    expect(alert.text()).toContain('Unable to list categories')
  })
})
