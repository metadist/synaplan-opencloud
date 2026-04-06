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

vi.mock('../../src/components/SummarizeDialog.vue', () => ({
  default: { name: 'SummarizeDialogStub' }
}))

import { useSummarizeExtension } from '../../src/extensions/useSummarizeExtension'
import type { FileActionOptions } from '@opencloud-eu/web-pkg'

function resource(overrides: Partial<{ isFolder: boolean; mimeType: string; id: string }> = {}) {
  return {
    id: 'r1',
    name: 'doc.txt',
    isFolder: false,
    mimeType: 'text/plain',
    ...overrides
  } as unknown as FileActionOptions['resources'][number]
}

describe('useSummarizeExtension', () => {
  beforeEach(() => {
    dispatchModal.mockReset()
    showErrorMessage.mockReset()
    userRef.user = { id: 'u1' }
  })

  it('registers against the files context-actions extension point', () => {
    const ext = useSummarizeExtension()
    expect(ext.id).toBe('com.synaplan.summarize')
    expect(ext.type).toBe('action')
    expect(ext.extensionPointIds).toContain('global.files.context-actions')
    expect(ext.action.class).toBe('oc-files-actions-summarize-trigger')
    expect(ext.action.icon).toBe('file-list-3')
    expect(ext.action.iconFillType).toBe('line')
  })

  describe('isVisible', () => {
    it('hides when the user is not signed in', () => {
      userRef.user = null
      const { action } = useSummarizeExtension()
      expect(action.isVisible({ resources: [resource()] } as FileActionOptions)).toBe(false)
    })

    it('hides folders', () => {
      const { action } = useSummarizeExtension()
      expect(
        action.isVisible({ resources: [resource({ isFolder: true })] } as FileActionOptions)
      ).toBe(false)
    })

    it.each([
      ['text/plain', true],
      ['application/pdf', true],
      ['application/vnd.oasis.opendocument.text', true],
      ['image/png', false],
      ['audio/mpeg', false],
      ['', false]
    ])('mime %s → %s', (mime, expected) => {
      const { action } = useSummarizeExtension()
      expect(
        action.isVisible({ resources: [resource({ mimeType: mime })] } as FileActionOptions)
      ).toBe(expected)
    })
  })

  describe('handler', () => {
    it('dispatches the summarize modal with the selected resource', () => {
      const { action } = useSummarizeExtension()
      const r = resource({ id: 'abc' })
      action.handler({ resources: [r] } as FileActionOptions)

      expect(dispatchModal).toHaveBeenCalledTimes(1)
      const modal = dispatchModal.mock.calls[0][0]
      expect(modal.title).toBe('Summarize with Synaplan')
      expect(modal.hideActions).toBe(true)
      expect(modal.customComponentAttrs()).toEqual({ resource: r })
    })

    it('errors out and does not dispatch when no resource was passed', () => {
      const { action } = useSummarizeExtension()
      action.handler({ resources: [] } as FileActionOptions)

      expect(dispatchModal).not.toHaveBeenCalled()
      expect(showErrorMessage).toHaveBeenCalledWith({ title: 'No file selected' })
    })
  })
})
