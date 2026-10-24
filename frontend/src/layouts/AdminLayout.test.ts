import { mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it } from 'vitest'

import { authState, resetAuthForTests } from '../stores/auth'
import AdminLayout from './AdminLayout.vue'

const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/', name: 'home', component: { template: '<div>home</div>' } },
    { path: '/admin', name: 'admin-overview', component: { template: '<div>overview</div>' } },
    { path: '/admin/credentials', name: 'admin-credentials', component: { template: '<div />' } },
    { path: '/admin/jobs', name: 'admin-jobs', component: { template: '<div />' } },
    { path: '/admin/categories', name: 'admin-categories', component: { template: '<div />' } },
    { path: '/admin/repositories', name: 'admin-repositories', component: { template: '<div />' } },
    { path: '/admin/operations', name: 'admin-operations', component: { template: '<div />' } },
    { path: '/admin/login', name: 'admin-login', component: { template: '<div>login</div>' } },
  ],
})

beforeEach(async () => {
  resetAuthForTests()
  authState.loaded = true
  authState.user = { username: 'admin' }
  await router.push('/admin')
  await router.isReady()
})

describe('AdminLayout 前后台往返', () => {
  it('后台 Logo 和返回前台入口都指向前台首页', () => {
    const wrapper = mount(AdminLayout, {
      global: { plugins: [router], stubs: { RouterView: true } },
    })

    const logo = wrapper.find('a[aria-label="返回 GitHub Radar 前台首页"]')
    expect(logo.exists()).toBe(true)
    expect(logo.attributes('href')).toBe('/')

    const backLink = wrapper.find('a[data-testid="back-to-public"]')
    expect(backLink.text()).toBe('返回前台')
    expect(backLink.attributes('href')).toBe('/')
  })
})
