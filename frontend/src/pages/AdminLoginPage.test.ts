import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'

import AdminLoginPage from './AdminLoginPage.vue'
import { resetAuthForTests } from '../stores/auth'

const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/admin/login', name: 'admin-login', component: AdminLoginPage },
    { path: '/admin', name: 'admin-overview', component: { template: '<div>overview</div>' } },
  ],
})

function mockFetchOnce(response: Response) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))
}

function fakeResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response
}

afterEach(() => {
  vi.unstubAllGlobals()
  resetAuthForTests()
})

describe('AdminLoginPage', () => {
  it('submits credentials and navigates to the admin overview', async () => {
    mockFetchOnce(
      fakeResponse(200, {
        data: { admin: { username: 'ops' }, csrf_token: 'csrf', expires_at: '2026-10-24T00:00:00Z' },
      }),
    )
    await router.push('/admin/login')
    await router.isReady()

    const wrapper = mount(AdminLoginPage, { global: { plugins: [router] } })
    await wrapper.find('#admin-username').setValue('ops')
    await wrapper.find('#admin-password').setValue('correct horse battery')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(router.currentRoute.value.path).toBe('/admin')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('shows the server error message when login fails', async () => {
    mockFetchOnce(
      fakeResponse(401, {
        error: { code: 'INVALID_CREDENTIALS', message: 'Invalid username or password' },
        request_id: 'req-login',
      }),
    )
    await router.push('/admin/login')
    await router.isReady()

    const wrapper = mount(AdminLoginPage, { global: { plugins: [router] } })
    await wrapper.find('#admin-username').setValue('ops')
    await wrapper.find('#admin-password').setValue('wrong-password')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    const alert = wrapper.find('[role="alert"]')
    expect(alert.exists()).toBe(true)
    expect(alert.text()).toContain('Invalid username or password')
    expect(router.currentRoute.value.path).toBe('/admin/login')
  })
})
