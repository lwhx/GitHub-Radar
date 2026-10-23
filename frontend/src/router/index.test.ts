import { describe, expect, it } from 'vitest'

import router from './index'

describe('admin routes', () => {
  it('registers dedicated credential and collection task pages', () => {
    expect(router.resolve('/admin/credentials').name).toBe('admin-credentials')
    expect(router.resolve('/admin/jobs').name).toBe('admin-jobs')
  })
})

describe('public repository routes', () => {
  it('registers the repository list and detail pages', () => {
    expect(router.resolve('/repos').name).toBe('repos')
    expect(router.resolve('/repos/acme/rocket').name).toBe('repo-detail')
  })
})

it('registers the categories page', () => {
  expect(router.resolve('/categories').name).toBe('categories')
})

it('registers the favorites page', () => {
  expect(router.resolve('/favorites').name).toBe('favorites')
})
