import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import HomePage from './HomePage.vue'

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

describe('HomePage', () => {
  it('shows the empty database guidance when no repositories are indexed', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        fakeResponse(200, {
          data: { repositories: 0, star_snapshots: 0, categories: 0, latest_sync_at: null, data_updated_at: null },
        }),
      ),
    )

    const wrapper = mount(HomePage)
    await flushPromises()

    expect(wrapper.text()).toContain('数据库为空')
    expect(wrapper.text()).toContain('不使用演示项目或假统计占位')
    expect(wrapper.text()).toContain('收录仓库')
  })

  it('hides the empty guidance once repositories exist', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        fakeResponse(200, {
          data: {
            repositories: 12,
            star_snapshots: 40,
            categories: 3,
            latest_sync_at: '2026-10-23T01:00:00Z',
            data_updated_at: '2026-10-23T01:00:00Z',
          },
        }),
      ),
    )

    const wrapper = mount(HomePage)
    await flushPromises()

    expect(wrapper.text()).not.toContain('数据库为空')
    expect(wrapper.text()).toContain('12')
  })

  it('shows an error state with retry when the stats request fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        fakeResponse(500, { error: { code: 'INTERNAL_ERROR', message: 'Unable to load statistics' } }),
      ),
    )

    const wrapper = mount(HomePage)
    await flushPromises()

    const alert = wrapper.find('[role="alert"]')
    expect(alert.exists()).toBe(true)
    expect(alert.text()).toContain('Unable to load statistics')
  })
})
