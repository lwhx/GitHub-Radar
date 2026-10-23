import { describe, expect, it } from 'vitest'

import router from './index'

describe('admin routes', () => {
  it('registers dedicated credential and collection task pages', () => {
    expect(router.resolve('/admin/credentials').name).toBe('admin-credentials')
    expect(router.resolve('/admin/jobs').name).toBe('admin-jobs')
  })
})
