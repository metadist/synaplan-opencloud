import { describe, expect, it, vi, beforeEach } from 'vitest'

const dispatchModal = vi.fn()
const userRef = { user: { id: 'u1' } as { id: string } | null }

vi.mock('@opencloud-eu/web-pkg', () => ({
  useModals: () => ({ dispatchModal }),
  useMessages: () => ({ showErrorMessage: vi.fn() }),
  useUserStore: () => userRef
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

vi.mock('../../src/components/WatchDialog.vue', () => ({ default: { name: 'WatchDialog' } }))

import { useWatchExtension } from '../../src/extensions/useWatchExtension'
import type { FileActionOptions } from '@opencloud-eu/web-pkg'

describe('useWatchExtension', () => {
  beforeEach(() => {
    userRef.user = { id: 'u1' }
  })

  it('is visible on a folder and on a file', () => {
    const { action } = useWatchExtension()
    expect(action.class).toBe('oc-files-actions-watch-trigger')
    expect(
      action.isVisible({ resources: [{ id: 'f', isFolder: true }] } as unknown as FileActionOptions)
    ).toBe(true)
    expect(
      action.isVisible({
        resources: [{ id: 'a', isFolder: false }]
      } as unknown as FileActionOptions)
    ).toBe(true)
  })

  it('hides when nobody is signed in', () => {
    userRef.user = null
    const { action } = useWatchExtension()
    expect(
      action.isVisible({ resources: [{ id: 'f', isFolder: true }] } as unknown as FileActionOptions)
    ).toBe(false)
  })
})
