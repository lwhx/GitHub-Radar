import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import router from '../router'
import RepoDetailPage from './RepoDetailPage.vue'

function fakeResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response
}

const sampleRepo = {
  full_name: 'acme/rocket',
  owner: 'acme',
  name: 'rocket',
  html_url: 'https://github.com/acme/rocket',
  homepage_url: 'https://rocket.dev',
  description: '快速部署的开源框架',
  language: 'Go',
  license_spdx: 'MIT',
  topics: ['web', 'framework'],
  stars_count: 77,
  forks_count: 9,
  open_issues_count: 4,
  is_fork: false,
  is_archived: false,
  github_created_at: '2026-01-01T00:00:00Z',
  github_updated_at: '2026-02-01T00:00:00Z',
  pushed_at: null,
  first_seen_at: '2026-01-03T00:00:00Z',
  last_synced_at: '2026-02-02T00:00:00Z',
}

async function mountAtRoute(path: string) {
  await router.push(path)
  await router.isReady()
  const wrapper = mount(RepoDetailPage, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('RepoDetailPage', () => {
  it('renders repository details from the public API', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(fakeResponse(200, { data: sampleRepo })))

    const wrapper = await mountAtRoute('/repos/acme/rocket')

    const text = wrapper.text()
    expect(text).toContain('acme/rocket')
    expect(text).toContain('快速部署的开源框架')
    expect(text).toContain('77')
    expect(text).toContain('Go')
    expect(text).toContain('MIT')
    expect(text).toContain('web')
    expect(text).toContain('2026-01-03')
    expect(text).toContain('最近同步')
  })

  it('shows a not-found state for unknown or hidden repositories', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(fakeResponse(404, { error: { code: 'REPO_NOT_FOUND', message: 'repository not found' } })),
    )

    const wrapper = await mountAtRoute('/repos/acme/ghost')

    expect(wrapper.text()).toContain('仓库不存在或已被隐藏')
    const back = wrapper.findAll('a').find((a) => a.attributes('href')?.includes('/repos'))
    expect(back).toBeTruthy()
  })

  it('toggles favorite from the detail header', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(fakeResponse(200, { data: sampleRepo })))

    const wrapper = await mountAtRoute('/repos/acme/rocket')
    const favorite = wrapper.findAll('button').find((button) => button.text().includes('收藏'))
    expect(favorite).toBeTruthy()
    await favorite!.trigger('click')

    const stored = JSON.parse(window.localStorage.getItem('radar-favorites') ?? '[]')
    expect(stored).toHaveLength(1)
    expect(stored[0].full_name).toBe('acme/rocket')
  })
})
