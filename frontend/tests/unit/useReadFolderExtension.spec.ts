import { describe, it, expect, vi, beforeEach } from 'vitest'

const showMessage = vi.fn()
const showErrorMessage = vi.fn()
const allowFolder = vi.fn()
const userRef = { user: null as { id: string } | null }

vi.mock('@opencloud-eu/web-pkg', () => ({
  useMessages: () => ({ showMessage, showErrorMessage }),
  useUserStore: () => userRef
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

vi.mock('../../src/composables/useReadScope', () => ({
  useReadScope: () => ({ allowFolder })
}))

import { useReadFolderExtension } from '../../src/extensions/useReadFolderExtension'
import type { FileActionOptions } from '@opencloud-eu/web-pkg'

function options(resources: Partial<{ id: string; path: string; isFolder: boolean }>[]) {
  return { resources } as unknown as FileActionOptions
}

describe('useReadFolderExtension', () => {
  beforeEach(() => {
    showMessage.mockReset()
    showErrorMessage.mockReset()
    allowFolder.mockReset()
    userRef.user = { id: 'u1' }
  })

  it('describes its action', () => {
    const ext = useReadFolderExtension()
    expect(ext.id).toBe('com.synaplan.read-folder')
    expect(ext.action.name).toBe('let-synaplan-read')
    expect(ext.action.class).toBe('oc-files-actions-let-synaplan-read-trigger')
    expect(ext.action.label()).toBe('Let Synaplan read this folder')
  })

  describe('isVisible', () => {
    it.each([
      ['one folder', [{ isFolder: true }], true],
      ['a file', [{ isFolder: false }], false],
      ['two folders', [{ isFolder: true }, { isFolder: true }], false],
      ['nothing', [], false]
    ])('%s → %s', (_, resources, expected) => {
      expect(useReadFolderExtension().action.isVisible(options(resources))).toBe(expected)
    })

    it('hides when nobody is signed in', () => {
      userRef.user = null
      expect(useReadFolderExtension().action.isVisible(options([{ isFolder: true }]))).toBe(false)
    })
  })

  describe('handler', () => {
    it('allows the folder and says so', async () => {
      allowFolder.mockResolvedValueOnce({ mode: 'allowed', folders: [{ id: 'f1', path: 'Specs' }] })
      await useReadFolderExtension().action.handler(
        options([{ id: 'f1', path: '/Specs', isFolder: true }])
      )
      expect(allowFolder).toHaveBeenCalledWith({ id: 'f1', path: '/Specs' })
      expect(showMessage).toHaveBeenCalledWith({ title: 'Synaplan may read this folder.' })
    })

    it('says so when the folder is gone', async () => {
      allowFolder.mockResolvedValueOnce({ mode: 'allowed', folders: [] })
      await useReadFolderExtension().action.handler(options([{ id: 'f1', isFolder: true }]))
      expect(showMessage).not.toHaveBeenCalled()
      expect(showErrorMessage).toHaveBeenCalledWith({
        title: 'This folder is no longer there. Reload the folder and try again.'
      })
    })

    it('shows the sentence for a failure', async () => {
      allowFolder.mockRejectedValueOnce({ response: { data: { code: 'cloud_not_linked' } } })
      await useReadFolderExtension().action.handler(options([{ id: 'f1', isFolder: true }]))
      expect(showErrorMessage).toHaveBeenCalledWith({
        title: 'Synaplan does not accept settings from this OpenCloud yet. Ask your administrator.'
      })
    })

    it('errors out when no folder was passed', async () => {
      await useReadFolderExtension().action.handler(options([]))
      expect(allowFolder).not.toHaveBeenCalled()
      expect(showErrorMessage).toHaveBeenCalledWith({ title: 'No folder selected' })
    })
  })
})
