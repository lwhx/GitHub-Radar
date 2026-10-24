import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it } from 'vitest'

import router from '../router'
import { authState, resetAuthForTests } from '../stores/auth'
import PublicLayout from './PublicLayout.vue'

beforeEach(() => {
  window.localStorage.clear()
  resetAuthForTests()
})

async function mountLayout() {
  await router.push('/')
  await router.isReady()
  // RouterView 用 stub：子页面会在 mounted 阶段发起 API 请求，与本布局测试无关。
  const wrapper = mount(PublicLayout, {
    global: { plugins: [router], stubs: { RouterView: true } },
  })
  await flushPromises()
  return wrapper
}

describe('PublicLayout 前后台入口', () => {
  it('不再把管理后台混入桌面与移动内容导航', async () => {
    const wrapper = await mountLayout()
    const desktopNav = wrapper.find('nav[aria-label="主导航"]')
    expect(desktopNav.text()).not.toContain('管理后台')

    const menuButton = wrapper.find('button[aria-label="打开导航菜单"]')
    await menuButton.trigger('click')
    await flushPromises()
    const drawer = wrapper.find('div[role="dialog"][aria-label="导航菜单"]')
    expect(drawer.text()).not.toContain('管理后台')
  })

  it('未登录时在右上角显示管理员登录入口', async () => {
    authState.loaded = true
    authState.user = null
    const wrapper = await mountLayout()

    const accountNav = wrapper.find('nav[aria-label="账户导航"]')
    const loginLink = accountNav.find('a')
    expect(loginLink.text()).toBe('管理员登录')
    expect(loginLink.attributes('href')).toBe('/admin/login')
  })

  it('已登录时在右上角显示用户名和管理后台入口', async () => {
    authState.loaded = true
    authState.user = { username: 'admin' }
    const wrapper = await mountLayout()

    const accountNav = wrapper.find('nav[aria-label="账户导航"]')
    expect(accountNav.text()).toContain('admin')
    const adminLink = accountNav.find('a')
    expect(adminLink.text()).toBe('管理后台')
    expect(adminLink.attributes('href')).toBe('/admin')
  })
})
