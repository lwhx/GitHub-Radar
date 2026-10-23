import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import router from '../router'
import ReposPage from './ReposPage.vue'

function fakeResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response
}

function repoItem(fullName: string, stars: number, description: string | null) {
  const [owner, name] = fullName.split('/')
  return {
    full_name: fullName,
    owner,
    name,
    html_url: `https://github.com/${fullName}`,
    homepage_url: null,
    description,
    language: 'Go',
    license_spdx: 'MIT',
    topics: [],
    stars_count: stars,
    forks_count: 2,
    open_issues_count: 1,
    is_fork: false,
    is_archived: false,
    github_created_at: '2026-01-02T00:00:00Z',
    github_updated_at: '2026-01-02T00:00:00Z',
    pushed_at: null,
    first_seen_at: '2026-01-03T00:00:00Z',
    last_synced_at: null,
  }
}

const sampleRepos = {
  items: [repoItem('beta/two', 100, '第二个项目'), repoItem('alpha/one', 5, null)],
  meta: { total: 2, page: 1, limit: 20, data_updated_at: '2026-10-23T01:00:00Z' },
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ReposPage', () => {
  it('renders collected repositories with links to detail pages', async () => {
    const fetchMock = vi.fn().mockResolvedValue(fakeResponse(200, { data: sampleRepos }))
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mount(ReposPage, { global: { plugins: [router] } })
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('beta/two')
    expect(text).toContain('100')
    expect(text).toContain('第二个项目')
    expect(text).toContain('共 2 个项目')
    const link = wrapper.findAll('a').find((a) => a.attributes('href')?.includes('/repos/beta/two'))
    expect(link).toBeTruthy()
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain('/api/v1/repos?')
  })

  it('shows the honest empty state when nothing is collected yet', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        fakeResponse(200, { data: { items: [], meta: { total: 0, page: 1, limit: 20, data_updated_at: null } } }),
      ),
    )

    const wrapper = mount(ReposPage, { global: { plugins: [router] } })
    await flushPromises()

    expect(wrapper.text()).toContain('暂无收录数据')
    expect(wrapper.text()).toContain('不使用演示项目')
  })

  it('shows an error alert with retry when the request fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(fakeResponse(500, { error: { code: 'INTERNAL_ERROR', message: 'Unable to list' } })),
    )

    const wrapper = mount(ReposPage, { global: { plugins: [router] } })
    await flushPromises()

    const alert = wrapper.find('[role="alert"]')
    expect(alert.exists()).toBe(true)
    expect(alert.text()).toContain('Unable to list')
  })

  it('queries the backend with the entered keyword on search', async () => {
    const fetchMock = vi.fn().mockResolvedValue(fakeResponse(200, { data: sampleRepos }))
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mount(ReposPage, { global: { plugins: [router] } })
    await flushPromises()
    await wrapper.find('input[type="search"]').setValue('agent')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    const lastUrl = String(fetchMock.mock.calls.at(-1)?.[0])
    expect(lastUrl).toContain('q=agent')
  })

  it('requests the next page through pagination controls', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      fakeResponse(200, {
        data: {
          items: [repoItem('high/stars', 3, null)],
          meta: { total: 2, page: 1, limit: 1, data_updated_at: null },
        },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mount(ReposPage, { global: { plugins: [router] } })
    await flushPromises()
    const next = wrapper.findAll('button').find((button) => button.text().trim() === '下一页')
    expect(next).toBeTruthy()
    await next!.trigger('click')
    await flushPromises()

    expect(String(fetchMock.mock.calls.at(-1)?.[0])).toContain('page=2')
  })
})

it('applies the category filter from the route query', async () => {
  const fetchMock = vi.fn().mockResolvedValue(fakeResponse(200, { data: sampleRepos }))
  vi.stubGlobal('fetch', fetchMock)

  await router.push('/repos?category=ai')
  await router.isReady()
  const wrapper = mount(ReposPage, { global: { plugins: [router] } })
  await flushPromises()

  expect(String(fetchMock.mock.calls[0]?.[0])).toContain('category=ai')
})
