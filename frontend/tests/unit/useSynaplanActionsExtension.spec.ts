import { describe, it, expect, vi, beforeEach } from 'vitest'

const userRef = { user: null as { id: string } | null }

vi.mock('@opencloud-eu/web-pkg', () => ({
  useModals: () => ({ dispatchModal: vi.fn() }),
  useMessages: () => ({ showErrorMessage: vi.fn() }),
  useUserStore: () => userRef,
  useConfigStore: () => ({ serverUrl: 'https://oc.example.com/' })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

import { useSynaplanActionsExtension } from '../../src/extensions/useSynaplanActionsExtension'
import type { FileActionOptions } from '@opencloud-eu/web-pkg'

function options(overrides: Partial<{ isFolder: boolean; mimeType: string }> = {}) {
  return {
    resources: [
      { id: 'r1', name: 'doc.txt', isFolder: false, mimeType: 'text/plain', ...overrides }
    ]
  } as unknown as FileActionOptions
}

describe('useSynaplanActionsExtension', () => {
  beforeEach(() => {
    userRef.user = { id: 'u1' }
  })

  it('registers a single group against the files context-actions extension point', () => {
    const ext = useSynaplanActionsExtension()
    expect(ext.id).toBe('com.synaplan.actions')
    expect(ext.type).toBe('action')
    expect(ext.extensionPointIds).toEqual(['global.files.context-actions'])
    expect(ext.action.name).toBe('synaplan')
    expect(ext.action.label()).toBe('Synaplan')
    expect(ext.action.handler).toBeUndefined()
  })

  it('uses the Synaplan brand icon for the group', () => {
    const { action } = useSynaplanActionsExtension()
    expect(action.icon).toEqual({
      src: 'https://oc.example.com/api/synaplan/assets/brand-icon?theme=light',
      srcDark: 'https://oc.example.com/api/synaplan/assets/brand-icon?theme=dark'
    })
  })

  it('groups the translate, summarize and knowledge actions', () => {
    const { action } = useSynaplanActionsExtension()
    expect(action.children.map((child) => child.name)).toEqual([
      'translate',
      'summarize',
      'add-to-knowledge'
    ])
  })

  describe('isVisible', () => {
    it('shows the group when a child is visible', () => {
      const { action } = useSynaplanActionsExtension()
      expect(action.isVisible(options())).toBe(true)
    })

    it.each([
      ['a folder', options({ isFolder: true })],
      ['an unsupported mime type', options({ mimeType: 'image/png' })],
      ['multiple resources', { resources: [{}, {}] } as unknown as FileActionOptions]
    ])('hides the group for %s', (_, opts) => {
      const { action } = useSynaplanActionsExtension()
      expect(action.isVisible(opts)).toBe(false)
    })

    it('hides the group when the user is not signed in', () => {
      userRef.user = null
      const { action } = useSynaplanActionsExtension()
      expect(action.isVisible(options())).toBe(false)
    })
  })
})
