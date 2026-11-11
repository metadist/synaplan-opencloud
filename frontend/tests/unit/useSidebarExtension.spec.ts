import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: {} })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

vi.mock('../../src/composables/useSynaplanBrandIcon', () => ({
  useSynaplanBrandIcon: () => 'bird.svg'
}))

import { cloudFilesEnabled } from '../../src/composables/cloudStatus'
import { useSidebarExtension } from '../../src/extensions/useSidebarExtension'

describe('useSidebarExtension', () => {
  const extension = useSidebarExtension()

  beforeEach(() => {
    cloudFilesEnabled.value = true
  })

  it('is a panel on the files sidebar', () => {
    expect(extension.type).toBe('sidebarPanel')
    expect(extension.extensionPointIds).toEqual(['global.files.sidebar'])
    expect(extension.panel.name).toBe('synaplan')
    expect(extension.panel.icon).toBe('bird.svg')
  })

  it('shows for one selected file and hands it to the panel', () => {
    const one = { id: 's!f1', isFolder: false }
    expect(extension.panel.isVisible({ items: [one] } as never)).toBe(true)
    expect(extension.panel.isVisible({ items: [one, { id: 's!f2' }] } as never)).toBe(false)
    expect(extension.panel.isVisible({ items: [] } as never)).toBe(false)
    expect(extension.panel.isVisible({ items: [{ id: 's!dir', isFolder: true }] } as never)).toBe(
      false
    )
    expect(extension.panel.componentAttrs?.({ items: [one] } as never)).toEqual({ resource: one })
  })

  it('stays hidden until cloud file state is on', () => {
    cloudFilesEnabled.value = false
    expect(extension.panel.isVisible({ items: [{ id: 's!f1' }] } as never)).toBe(false)
  })
})
