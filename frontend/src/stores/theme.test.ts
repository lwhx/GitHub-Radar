import { beforeEach, describe, expect, it } from 'vitest'

import { applyTheme, isDark, readSavedTheme, resolveInitialTheme, theme, toggleTheme } from './theme'

describe('resolveInitialTheme', () => {
  it('uses saved preference when it is a valid theme value', () => {
    expect(resolveInitialTheme({ getItem: () => 'dark' }, false)).toBe('dark')
    expect(resolveInitialTheme({ getItem: () => 'light' }, true)).toBe('light')
  })

  it('falls back to the system preference when nothing is saved', () => {
    expect(resolveInitialTheme({ getItem: () => null }, true)).toBe('dark')
    expect(resolveInitialTheme({ getItem: () => null }, false)).toBe('light')
  })

  it('ignores invalid saved values', () => {
    expect(resolveInitialTheme({ getItem: () => 'purple' }, true)).toBe('dark')
    expect(resolveInitialTheme({ getItem: () => '' }, false)).toBe('light')
  })
})

describe('theme store', () => {
  beforeEach(() => {
    applyTheme('light')
  })

  it('toggle switches the theme, updates the document class, and persists', () => {
    expect(theme.value).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)

    toggleTheme()

    expect(theme.value).toBe('dark')
    expect(isDark.value).toBe(true)
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(readSavedTheme()).toBe('dark')

    toggleTheme()

    expect(theme.value).toBe('light')
    expect(readSavedTheme()).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })
})
