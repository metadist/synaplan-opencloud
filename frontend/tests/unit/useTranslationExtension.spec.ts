import { describe, it, expect, vi, beforeEach } from 'vitest'

// --- Module mocks ---------------------------------------------------
//
// useTranslationExtension pulls several composables from
// @opencloud-eu/web-pkg at call time. The test only cares about the
// shape the extension observes, not their real implementations — so
// stub the whole module with the minimum surface the extension
// touches.

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

// The dialog is dynamically imported — defineAsyncComponent reads the
// factory lazily, so we never actually hit this import in the tests
// below. Still stub it so vite-node doesn't try to resolve the .vue.
vi.mock('../../src/components/TranslationDialog.vue', () => ({
  default: { name: 'TranslationDialogStub' }
}))

import { useTranslationExtension } from '../../src/extensions/useTranslationExtension'
import type { FileActionOptions } from '@opencloud-eu/web-pkg'

// Build a minimal Resource-shaped object. The extension only reads
// isFolder, mimeType, id, and name, so a duck-typed object is enough
// — we don't need to pull the real Resource class.
function resource(overrides: Partial<{ isFolder: boolean; mimeType: string; id: string }> = {}) {
  return {
    id: 'r1',
    name: 'doc.txt',
    isFolder: false,
    mimeType: 'text/plain',
    ...overrides
  } as unknown as FileActionOptions['resources'][number]
}

describe('useTranslationExtension', () => {
  beforeEach(() => {
    dispatchModal.mockReset()
    showErrorMessage.mockReset()
    userRef.user = { id: 'u1' }
  })

  it('registers itself against the files context-actions extension point', () => {
    const ext = useTranslationExtension()
    expect(ext.id).toBe('com.synaplan.translation')
    expect(ext.type).toBe('action')
    expect(ext.extensionPointIds).toContain('global.files.context-actions')
    expect(ext.action.class).toBe('oc-files-actions-translate-trigger')
  })

  describe('isVisible', () => {
    it('hides when the user is not signed in', () => {
      userRef.user = null
      const { action } = useTranslationExtension()
      expect(action.isVisible({ resources: [resource()] } as FileActionOptions)).toBe(false)
    })

    it('hides when no resource is selected', () => {
      const { action } = useTranslationExtension()
      expect(action.isVisible({ resources: [] } as FileActionOptions)).toBe(false)
    })

    it('hides when more than one resource is selected', () => {
      const { action } = useTranslationExtension()
      expect(
        action.isVisible({
          resources: [resource(), resource({ id: 'r2' })]
        } as FileActionOptions)
      ).toBe(false)
    })

    it('hides folders', () => {
      const { action } = useTranslationExtension()
      expect(
        action.isVisible({ resources: [resource({ isFolder: true })] } as FileActionOptions)
      ).toBe(false)
    })

    it.each([
      ['text/plain', true],
      ['text/markdown', true],
      ['application/pdf', true],
      ['application/json', true],
      ['application/xml', true],
      ['application/rtf', true],
      ['application/msword', true],
      ['application/vnd.openxmlformats-officedocument.wordprocessingml.document', true],
      ['application/vnd.oasis.opendocument.text', true],
      ['image/png', false],
      ['audio/mpeg', false],
      ['video/mp4', false],
      ['application/octet-stream', false],
      ['', false]
    ])('returns %s → %s', (mime, expected) => {
      const { action } = useTranslationExtension()
      expect(
        action.isVisible({ resources: [resource({ mimeType: mime })] } as FileActionOptions)
      ).toBe(expected)
    })
  })

  describe('handler', () => {
    it('dispatches the translation modal with the selected resource', () => {
      const { action } = useTranslationExtension()
      const r = resource({ id: 'abc', mimeType: 'text/plain' })
      action.handler({ resources: [r] } as FileActionOptions)

      expect(dispatchModal).toHaveBeenCalledTimes(1)
      const modal = dispatchModal.mock.calls[0][0]
      expect(modal.title).toBe('Translate with Synaplan')
      expect(modal.hideActions).toBe(true)
      expect(modal.customComponentAttrs()).toEqual({ resource: r })
    })

    it('shows an error and does not dispatch when no resource was passed', () => {
      const { action } = useTranslationExtension()
      action.handler({ resources: [] } as FileActionOptions)

      expect(dispatchModal).not.toHaveBeenCalled()
      expect(showErrorMessage).toHaveBeenCalledWith({ title: 'No file selected' })
    })
  })
})
