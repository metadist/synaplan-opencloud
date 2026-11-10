import { describe, expect, it, vi } from 'vitest'

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: {} })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

vi.mock('../../src/composables/useSynaplanBrandIcon', () => ({
  useSynaplanBrandIcon: () => 'bird.svg'
}))

import { useSidebarExtension } from '../../src/extensions/useSidebarExtension'

describe('useSidebarExtension', () => {
  const extension = useSidebarExtension()

  it('is a panel on the files sidebar', () => {
    expect(extension.type).toBe('sidebarPanel')
    expect(extension.extensionPointIds).toEqual(['global.files.sidebar'])
    expect(extension.panel.name).toBe('synaplan')
    expect(extension.panel.icon).toBe('bird.svg')
  })

  it('shows for one selected item and hands it to the panel', () => {
    const one = { id: 's!f1' }
    expect(extension.panel.isVisible({ items: [one] } as never)).toBe(true)
    expect(extension.panel.isVisible({ items: [one, { id: 's!f2' }] } as never)).toBe(false)
    expect(extension.panel.isVisible({ items: [] } as never)).toBe(false)
    expect(extension.panel.componentAttrs?.({ items: [one] } as never)).toEqual({ resource: one })
  })
})
