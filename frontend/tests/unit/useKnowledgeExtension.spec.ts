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

vi.mock('../../src/components/KnowledgeDialog.vue', () => ({
  default: { name: 'KnowledgeDialogStub' }
}))

import { useKnowledgeExtension } from '../../src/extensions/useKnowledgeExtension'
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

describe('useKnowledgeExtension', () => {
  beforeEach(() => {
    dispatchModal.mockReset()
    showErrorMessage.mockReset()
    userRef.user = { id: 'u1' }
  })

  it('registers against the files context-actions extension point', () => {
    const ext = useKnowledgeExtension()
    expect(ext.id).toBe('com.synaplan.knowledge')
    expect(ext.type).toBe('action')
    expect(ext.extensionPointIds).toContain('global.files.context-actions')
    expect(ext.action.class).toBe('oc-files-actions-add-to-knowledge-trigger')
    expect(ext.action.icon).toBe('brain')
    expect(ext.action.iconFillType).toBe('line')
  })

  describe('isVisible', () => {
    it('hides when the user is not signed in', () => {
      userRef.user = null
      const { action } = useKnowledgeExtension()
      expect(action.isVisible({ resources: [resource()] } as FileActionOptions)).toBe(false)
    })

    it('hides folders', () => {
      const { action } = useKnowledgeExtension()
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
      const { action } = useKnowledgeExtension()
      expect(
        action.isVisible({ resources: [resource({ mimeType: mime })] } as FileActionOptions)
      ).toBe(expected)
    })
  })

  describe('handler', () => {
    it('dispatches the knowledge modal with the selected resource', () => {
      const { action } = useKnowledgeExtension()
      const r = resource({ id: 'abc' })
      action.handler({ resources: [r] } as FileActionOptions)

      expect(dispatchModal).toHaveBeenCalledTimes(1)
      const modal = dispatchModal.mock.calls[0][0]
      expect(modal.title).toBe('Add to Synaplan knowledge')
      expect(modal.hideActions).toBe(true)
      expect(modal.customComponentAttrs()).toEqual({ resource: r })
    })

    it('errors out and does not dispatch when no resource was passed', () => {
      const { action } = useKnowledgeExtension()
      action.handler({ resources: [] } as FileActionOptions)

      expect(dispatchModal).not.toHaveBeenCalled()
      expect(showErrorMessage).toHaveBeenCalledWith({ title: 'No file selected' })
    })
  })
})
