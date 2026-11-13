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

vi.mock('../../src/components/ChatDialog.vue', () => ({
  default: { name: 'ChatDialogStub' }
}))

import { useChatExtension } from '../../src/extensions/useChatExtension'
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

describe('useChatExtension', () => {
  beforeEach(() => {
    dispatchModal.mockReset()
    showErrorMessage.mockReset()
    userRef.user = { id: 'u1' }
  })

  it('describes its action', () => {
    const ext = useChatExtension()
    expect(ext.id).toBe('com.synaplan.chat')
    expect(ext.type).toBe('action')
    expect(ext.action.name).toBe('chat-about-file')
    expect(ext.action.class).toBe('oc-files-actions-chat-about-file-trigger')
    expect(ext.action.label()).toBe('Ask about this file…')
  })

  describe('isVisible', () => {
    it('hides when the user is not signed in', () => {
      userRef.user = null
      const { action } = useChatExtension()
      expect(action.isVisible({ resources: [resource()] } as FileActionOptions)).toBe(false)
    })

    it.each([
      ['text/plain', false, true],
      ['application/pdf', false, true],
      ['image/png', false, false],
      ['text/plain', true, false]
    ])('mime %s, folder %s → %s', (mime, isFolder, expected) => {
      const { action } = useChatExtension()
      expect(
        action.isVisible({
          resources: [resource({ mimeType: mime, isFolder })]
        } as FileActionOptions)
      ).toBe(expected)
    })
  })

  describe('handler', () => {
    it('dispatches the chat dialog with the selected resource', () => {
      const { action } = useChatExtension()
      const r = resource({ id: 'abc' })
      action.handler({ resources: [r] } as FileActionOptions)

      expect(dispatchModal).toHaveBeenCalledTimes(1)
      const modal = dispatchModal.mock.calls[0][0]
      expect(modal.title).toBe('Ask Synaplan about this file')
      expect(modal.hideActions).toBe(true)
      expect(modal.customComponentAttrs()).toEqual({ resource: r })
    })

    it('errors out when no resource was passed', () => {
      const { action } = useChatExtension()
      action.handler({ resources: [] } as FileActionOptions)
      expect(dispatchModal).not.toHaveBeenCalled()
      expect(showErrorMessage).toHaveBeenCalledWith({ title: 'No file selected' })
    })
  })
})
