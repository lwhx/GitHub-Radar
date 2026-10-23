import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import router from '../router'
import RankingsPage from './RankingsPage.vue'

function fakeResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response
}

function repo(overrides: Record<string, unknown> = {}) {
  return {
    full_name: 'acme/rocket',
    owner: 'acme',
    name: 'rocket',
    html_url: 'https://github.com/acme/rocket',
    homepage_url: null,
    description: '快速部署的开源框架',
    language: 'Go',
    license_spdx: 'MIT',
    topics: [],
    stars_count: 180,
    forks_count: 12,
    open_issues_count: 3,
    is_fork: false,
    is_archived: false,
    github_created_at: '2026-10-01T00:00:00Z',
    github_updated_at: '2026-10-20T00:00:00Z',
    pushed_at: null,
    first_seen_at: '2026-10-20T00:00:00Z',
    last_synced_at: null,
    star_delta: null,
    score: null,
    score_reason: null,
    ...overrides,
  }
}

function meta(overrides: Record<string, unknown> = {}) {
  return {
    type: 'latest',
    window_hours: 0,
    page: 1,
    limit: 20,
    total: 1,
    data_updated_at: '2026-10-25T10:00:00Z',
    history_available: true,
    note: '按 GitHub 创建时间降序，同时间按 github_id 稳定排序',
    ...overrides,
  }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

async function mountAt(path: string) {
  await router.push(path)
  await router.isReady()
  const wrapper = mount(RankingsPage, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('RankingsPage', () => {
  it('renders the latest board with creation dates and all four tabs', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(fakeResponse(200, { data: { items: [repo()], meta: meta() } })),
    )

    const wrapper = await mountAt('/rankings')
    const text = wrapper.text()
    expect(text).toContain('最新项目')
    expect(text).toContain('24h 飙升')
    expect(text).toContain('7 日增长')
    expect(text).toContain('潜力黑马')
    expect(text).toContain('acme/rocket')
    expect(text).toContain('2026-10-01')
  })

  it('shows rolling star growth on the 24h board', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        fakeResponse(200, {
          data: {
            items: [
              repo({ full_name: 'acme/hot', stars_count: 180, star_delta: 80 }),
              repo({ full_name: 'acme/warm', stars_count: 70, star_delta: 20 }),
            ],
            meta: meta({ type: '24h', window_hours: 24, total: 2, note: '按滚动 24 小时净 Star 变化降序' }),
          },
        }),
      ),
    )

    const wrapper = await mountAt('/rankings?type=24h')
    const text = wrapper.text()
    expect(text).toContain('+80')
    expect(text).toContain('+20')
    expect(text).toContain('净 Star 变化')
    expect(String((vi.mocked(fetch) as unknown as ReturnType<typeof vi.fn>).mock.calls[0]?.[0])).toContain('type=24h')
  })

  it('shows an explicit insufficient-data state without fake zeros', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        fakeResponse(200, {
          data: {
            items: [],
            meta: meta({
              type: '24h',
              window_hours: 24,
              total: 0,
              history_available: false,
              note: '数据不足：增长窗口无有效历史样本，快照积累后自动填充（不会显示 0）',
            }),
          },
        }),
      ),
    )

    const wrapper = await mountAt('/rankings?type=24h')
    const text = wrapper.text()
    expect(text).toContain('数据不足')
    expect(text).not.toContain('+0')
  })
})
