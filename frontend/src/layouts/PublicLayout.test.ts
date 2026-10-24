import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it } from 'vitest'

import router from '../router'
import PublicLayout from './PublicLayout.vue'

beforeEach(() => {
  window.localStorage.clear()
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

describe('PublicLayout 侧栏导航', () => {
  it('桌面侧栏在「我的关注」之后渲染「管理后台」链接并指向 /admin/login', async () => {
    const wrapper = await mountLayout()

    const desktopNav = wrapper.find('nav[aria-label="主导航"]')
    expect(desktopNav.exists()).toBe(true)
    const labels = desktopNav.findAll('a').map((a) => a.text().trim())
    const adminIndex = labels.indexOf('管理后台')
    expect(adminIndex).toBeGreaterThan(-1)
    expect(labels[adminIndex - 1]).toBe('我的关注')
    const adminLink = desktopNav.findAll('a')[adminIndex]
    expect(adminLink.attributes('href')).toBe('/admin/login')
  })

  it('移动端抽屉在「我的关注」之后渲染「管理后台」链接并指向 /admin/login', async () => {
    const wrapper = await mountLayout()

    const menuButton = wrapper.find('button[aria-label="打开导航菜单"]')
    expect(menuButton.exists()).toBe(true)
    await menuButton.trigger('click')
    await flushPromises()

    const drawer = wrapper.find('div[role="dialog"][aria-label="导航菜单"]')
    expect(drawer.exists()).toBe(true)
    const labels = drawer.findAll('a').map((a) => a.text().trim())
    const adminIndex = labels.indexOf('管理后台')
    expect(adminIndex).toBeGreaterThan(-1)
    expect(labels[adminIndex - 1]).toBe('我的关注')
    const adminLink = drawer.findAll('a')[adminIndex]
    expect(adminLink.attributes('href')).toBe('/admin/login')
  })
})
