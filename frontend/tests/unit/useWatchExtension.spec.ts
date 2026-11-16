import { describe, it, expect, vi, beforeEach } from 'vitest'

const dispatchModal = vi.fn()
const showErrorMessage = vi.fn()
const userRef = { user: null as { id: string } | null }

vi.mock('@opencloud-eu/web-pkg', () => ({
  useModals: () => ({ dispatchModal }),
  useMessages: () => ({ showErrorMessage }),
  useUserStore: () => userRef
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

vi.mock('../../src/components/WatchDialog.vue', () => ({
  default: { name: 'WatchDialogStub' }
}))

import { useWatchExtension } from '../../src/extensions/useWatchExtension'
import type { FileActionOptions } from '@opencloud-eu/web-pkg'

function resource(overrides: Partial<{ isFolder: boolean; mimeType: string; id: string }> = {}) {
  return {
    id: 'r1',
    name: 'Inbox',
    isFolder: true,
    ...overrides
  } as unknown as FileActionOptions['resources'][number]
}

describe('useWatchExtension', () => {
  beforeEach(() => {
    dispatchModal.mockReset()
    showErrorMessage.mockReset()
    userRef.user = { id: 'u1' }
  })

  it('describes its action', () => {
    const ext = useWatchExtension()
    expect(ext.id).toBe('com.synaplan.watch')
    expect(ext.type).toBe('action')
    expect(ext.action.name).toBe('watch')
    expect(ext.action.class).toBe('oc-files-actions-watch-trigger')
    expect(ext.action.icon).toBe('eye')
    expect(ext.action.label()).toBe('Watch with Synaplan…')
  })

  describe('isVisible', () => {
    it.each([
      ['a folder', [resource()], true],
      ['a file of any type', [resource({ isFolder: false, mimeType: 'image/png' })], true],
      ['two items', [resource(), resource({ id: 'r2' })], false],
      ['nothing', [], false]
    ])('%s → %s', (_, resources, expected) => {
      const { action } = useWatchExtension()
      expect(action.isVisible({ resources } as FileActionOptions)).toBe(expected)
    })

    it('hides when the user is not signed in', () => {
      userRef.user = null
      const { action } = useWatchExtension()
      expect(action.isVisible({ resources: [resource()] } as FileActionOptions)).toBe(false)
    })
  })

  describe('handler', () => {
    it.each([
      ['folder', true],
      ['file', false]
    ])('opens the watch dialog for a %s', (kind, isFolder) => {
      const { action } = useWatchExtension()
      const r = resource({ isFolder })
      action.handler({ resources: [r] } as FileActionOptions)

      expect(dispatchModal).toHaveBeenCalledTimes(1)
      const modal = dispatchModal.mock.calls[0][0]
      expect(modal.title).toBe('Watch with Synaplan')
      expect(modal.hideActions).toBe(true)
      expect(modal.customComponentAttrs()).toEqual({ resource: r, kind })
    })

    it('errors out and does not dispatch when no resource was passed', () => {
      const { action } = useWatchExtension()
      action.handler({ resources: [] } as FileActionOptions)

      expect(dispatchModal).not.toHaveBeenCalled()
      expect(showErrorMessage).toHaveBeenCalledWith({ title: 'No file selected' })
    })
  })
})
