import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import AdminJobsPage from './AdminJobsPage.vue'

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

describe('AdminJobsPage', () => {
  it('loads the collection job and real run history', async () => {
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(
        fakeResponse(200, {
          data: {
            jobs: [
              {
                id: 1,
                job_type: 'github_collection',
                enabled: true,
                interval_seconds: 1800,
                next_run_at: '2026-10-23T12:30:00Z',
                lease_owner: null,
                lease_expires_at: null,
              },
              {
                id: 2,
                job_type: 'github_recovery',
                enabled: true,
                interval_seconds: 86400,
                next_run_at: '2026-10-24T12:00:00Z',
                lease_owner: null,
                lease_expires_at: null,
              },
            ],
          },
        }),
      )
      .mockResolvedValueOnce(
        fakeResponse(200, {
          data: {
            runs: [
              {
                id: 9,
                job_id: 1,
                status: 'deferred',
                trigger: 'recovery',
                started_at: '2026-10-23T12:00:00Z',
                finished_at: '2026-10-23T12:01:00Z',
                request_count: 5,
                created_count: 12,
                updated_count: 3,
                snapshot_count: 2,
                not_modified_count: 3,
                retry_count: 0,
                rate_limited_count: 1,
                failed_count: 0,
                coverage_incomplete_count: 1,
                last_error_code: null,
                error_summary: null,
              },
            ],
          },
        }),
      )
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminJobsPage)
    await flushPromises()

    expect(fetchSpy).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('每 30 分钟')
    expect(wrapper.text()).toContain('每日覆盖补采')
    expect(wrapper.text()).toContain('每 24 小时')
    expect(wrapper.text()).toContain('#9')
    expect(wrapper.text()).toContain('已延后')
    expect(wrapper.text()).toContain('恢复')
    expect(wrapper.text()).toContain('新增 12')
    expect(wrapper.text()).toContain('快照 2')
    expect(wrapper.text()).toContain('未变化 3')
    expect(wrapper.text()).toContain('限流 1')
    expect(wrapper.text()).toContain('覆盖不完整 1')
  })

  it('starts a manual run with CSRF and reports accepted state', async () => {
    document.cookie = 'radar_csrf=csrf-run; Path=/'
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { jobs: [] } }))
      .mockResolvedValueOnce(fakeResponse(200, { data: { runs: [] } }))
      .mockResolvedValueOnce(fakeResponse(202, { data: { status: 'accepted' } }))
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminJobsPage)
    await flushPromises()
    await wrapper.get('[data-test="run-now"]').trigger('click')
    await flushPromises()

    const [url, init] = fetchSpy.mock.calls[2] as unknown as [string, { method: string; headers: Record<string, string> }]
    expect(url).toBe('/api/v1/admin/jobs/run')
    expect(init.method).toBe('POST')
    expect(init.headers['X-CSRF-Token']).toBe('csrf-run')
    expect(wrapper.get('[role="status"]').text()).toContain('已提交')
  })

  it('explains how to fix a missing credential instead of echoing the raw error', async () => {
    document.cookie = 'radar_csrf=csrf-run; Path=/'
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(fakeResponse(200, { data: { jobs: [] } }))
      .mockResolvedValueOnce(fakeResponse(200, { data: { runs: [] } }))
      .mockResolvedValueOnce(
        fakeResponse(409, {
          error: { code: 'CREDENTIAL_REQUIRED', message: 'Add and verify a GitHub token before starting collection' },
          request_id: 'req-409',
        }),
      )
    vi.stubGlobal('fetch', fetchSpy)

    const wrapper = mount(AdminJobsPage)
    await flushPromises()
    await wrapper.get('[data-test="run-now"]').trigger('click')
    await flushPromises()

    const alert = wrapper.get('[role="alert"]')
    expect(alert.text()).toContain('凭据管理')
    expect(alert.text()).toContain('GitHub Token')
  })
})
