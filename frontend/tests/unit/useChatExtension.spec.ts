import { describe, expect, it, vi, beforeEach } from 'vitest'

const dispatchModal = vi.fn()
const showErrorMessage = vi.fn()
const userRef = { user: { id: 'u1' } as { id: string } | null }

vi.mock('@opencloud-eu/web-pkg', () => ({
  useModals: () => ({ dispatchModal }),
  useMessages: () => ({ showErrorMessage }),
  useUserStore: () => userRef
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

vi.mock('../../src/components/ChatDialog.vue', () => ({ default: { name: 'ChatDialog' } }))

import { useChatExtension } from '../../src/extensions/useChatExtension'
import type { FileActionOptions } from '@opencloud-eu/web-pkg'

function resource(overrides: Partial<{ isFolder: boolean; mimeType: string }> = {}) {
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
    userRef.user = { id: 'u1' }
  })

  it('is visible for one supported file', () => {
    const { action } = useChatExtension()
    expect(action.class).toBe('oc-files-actions-chat-about-file-trigger')
    expect(action.isVisible({ resources: [resource()] } as FileActionOptions)).toBe(true)
    expect(
      action.isVisible({ resources: [resource({ isFolder: true })] } as FileActionOptions)
    ).toBe(false)
  })

  it('opens the chat dialog', () => {
    const { action } = useChatExtension()
    action.handler?.({ resources: [resource()] } as FileActionOptions)
    expect(dispatchModal).toHaveBeenCalledOnce()
  })
})
