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

vi.mock('../../src/components/ReadFolderDialog.vue', () => ({
  default: { name: 'ReadFolderDialogStub' }
}))

import { useReadFolderExtension } from '../../src/extensions/useReadFolderExtension'
import type { FileActionOptions } from '@opencloud-eu/web-pkg'

function resource(overrides: Partial<{ isFolder: boolean; id: string }> = {}) {
  return {
    id: 'r1',
    name: 'Invoices',
    isFolder: true,
    ...overrides
  } as unknown as FileActionOptions['resources'][number]
}

describe('useReadFolderExtension', () => {
  beforeEach(() => {
    dispatchModal.mockReset()
    showErrorMessage.mockReset()
    userRef.user = { id: 'u1' }
  })

  it('describes its action', () => {
    const ext = useReadFolderExtension()
    expect(ext.id).toBe('com.synaplan.read-folder')
    expect(ext.type).toBe('action')
    expect(ext.action.name).toBe('read-folder')
    expect(ext.action.class).toBe('oc-files-actions-read-folder-trigger')
    expect(ext.action.icon).toBe('folder-open')
    expect(ext.action.label()).toBe('Let Synaplan read this folder…')
  })

  describe('isVisible', () => {
    it.each([
      ['a folder', [resource()], true],
      ['a file', [resource({ isFolder: false })], false],
      ['two folders', [resource(), resource({ id: 'r2' })], false],
      ['nothing', [], false]
    ])('%s → %s', (_, resources, expected) => {
      const { action } = useReadFolderExtension()
      expect(action.isVisible({ resources } as FileActionOptions)).toBe(expected)
    })

    it('hides when the user is not signed in', () => {
      userRef.user = null
      const { action } = useReadFolderExtension()
      expect(action.isVisible({ resources: [resource()] } as FileActionOptions)).toBe(false)
    })
  })

  describe('handler', () => {
    it('opens the read-folder dialog for the folder', () => {
      const { action } = useReadFolderExtension()
      const r = resource()
      action.handler({ resources: [r] } as FileActionOptions)

      expect(dispatchModal).toHaveBeenCalledTimes(1)
      const modal = dispatchModal.mock.calls[0][0]
      expect(modal.title).toBe('Let Synaplan read this folder')
      expect(modal.hideActions).toBe(true)
      expect(modal.customComponentAttrs()).toEqual({ resource: r })
    })

    it('errors out and does not dispatch when no folder was passed', () => {
      const { action } = useReadFolderExtension()
      action.handler({ resources: [] } as FileActionOptions)

      expect(dispatchModal).not.toHaveBeenCalled()
      expect(showErrorMessage).toHaveBeenCalledWith({ title: 'No folder selected' })
    })
  })
})
