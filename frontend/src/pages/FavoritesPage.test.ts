import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it } from 'vitest'

import router from '../router'
import { favorites, toggleFavorite } from '../stores/favorites'
import FavoritesPage from './FavoritesPage.vue'

const sample = {
  full_name: 'acme/rocket',
  owner: 'acme',
  name: 'rocket',
  html_url: 'https://github.com/acme/rocket',
  description: '快速部署的开源框架',
  language: 'Go',
  stars_count: 77,
}

beforeEach(() => {
  window.localStorage.clear()
  favorites.value = []
})

async function mountPage() {
  await router.push('/favorites')
  await router.isReady()
  const wrapper = mount(FavoritesPage, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('FavoritesPage', () => {
  it('renders locally favorited repositories with remove buttons', async () => {
    toggleFavorite(sample)

    const wrapper = await mountPage()

    const text = wrapper.text()
    expect(text).toContain('acme/rocket')
    expect(text).toContain('77')
    expect(text).toContain('快速部署的开源框架')
    const link = wrapper.findAll('a').find((a) => a.attributes('href')?.includes('/repos/acme/rocket'))
    expect(link).toBeTruthy()
    const remove = wrapper.findAll('button').find((button) => button.text().trim() === '移除')
    expect(remove).toBeTruthy()
    await remove!.trigger('click')
    expect(wrapper.text()).not.toContain('acme/rocket')
    expect(window.localStorage.getItem('radar-favorites')).toBe('[]')
  })

  it('shows the empty state when nothing is favorited yet', async () => {
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('还没有收藏的项目')
    expect(wrapper.text()).toContain('收藏只保存在当前浏览器')
  })
})
