import { beforeEach, describe, expect, it } from 'vitest'

import { favoriteCount, favorites, isFavorited, removeFavorite, toggleFavorite } from './favorites'

const sample = {
  full_name: 'acme/rocket',
  owner: 'acme',
  name: 'rocket',
  html_url: 'https://github.com/acme/rocket',
  description: null,
  language: 'Go',
  stars_count: 10,
}

beforeEach(() => {
  window.localStorage.clear()
  favorites.value = []
})

describe('favorites store', () => {
  it('toggles a repository and persists it to localStorage', () => {
    expect(isFavorited('acme/rocket')).toBe(false)
    expect(toggleFavorite(sample)).toBe(true)
    expect(isFavorited('acme/rocket')).toBe(true)

    const stored = JSON.parse(window.localStorage.getItem('radar-favorites') ?? '[]')
    expect(stored).toHaveLength(1)
    expect(stored[0].full_name).toBe('acme/rocket')
    expect(stored[0].favorited_at).toBeTruthy()

    expect(toggleFavorite(sample)).toBe(false)
    expect(isFavorited('acme/rocket')).toBe(false)
    expect(JSON.parse(window.localStorage.getItem('radar-favorites') ?? '[]')).toHaveLength(0)
  })

  it('survives corrupted storage content', () => {
    window.localStorage.setItem('radar-favorites', '{not json')
    expect(favoriteCount()).toBe(0)
    expect(toggleFavorite(sample)).toBe(true)
    expect(favoriteCount()).toBe(1)
  })

  it('removeFavorite drops one entry without touching others', () => {
    toggleFavorite(sample)
    toggleFavorite({ ...sample, full_name: 'beta/two', owner: 'beta', name: 'two' })
    removeFavorite('acme/rocket')
    expect(favoriteCount()).toBe(1)
    expect(isFavorited('beta/two')).toBe(true)
  })
})
