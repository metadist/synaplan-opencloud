import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { FileActionOptions } from '@opencloud-eu/web-pkg'

const userRef = { user: { id: 'u1' } as { id: string } | null }
const dispatchModal = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useModals: () => ({ dispatchModal }),
  useMessages: () => ({ showErrorMessage: vi.fn() }),
  useUserStore: () => userRef
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

import { useFromSynaplanExtension } from '../../src/extensions/useFromSynaplanExtension'

const folder = { id: 's!folder', isFolder: true }
const file = { id: 's!file', isFolder: false }
const opts = (...resources: object[]) => ({ resources }) as unknown as FileActionOptions

describe('useFromSynaplanExtension', () => {
  beforeEach(() => {
    userRef.user = { id: 'u1' }
    dispatchModal.mockReset()
  })

  it('is offered for one folder only', () => {
    const { action } = useFromSynaplanExtension()
    expect(action.isVisible(opts(folder))).toBe(true)
    expect(action.isVisible(opts(file))).toBe(false)
    expect(action.isVisible(opts(folder, folder))).toBe(false)
    userRef.user = null
    expect(action.isVisible(opts(folder))).toBe(false)
  })

  it('opens the dialog for that folder', () => {
    const { action } = useFromSynaplanExtension()
    action.handler(opts(folder))
    expect(dispatchModal).toHaveBeenCalledTimes(1)
    const modal = dispatchModal.mock.calls[0][0]
    expect(modal.title).toBe('From Synaplan')
    expect(modal.customComponentAttrs()).toEqual({ resource: folder })
  })
})
