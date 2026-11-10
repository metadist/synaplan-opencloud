import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Resource } from '@opencloud-eu/web-client'

const post = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { post } })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

vi.mock('../../src/composables/useSynaplanBrandIcon', () => ({
  useSynaplanBrandIcon: () => 'bird.svg'
}))

import { useBirdIndicator } from '../../src/extensions/useBirdIndicator'
import { cloudMarks, invalidateCloudStatus } from '../../src/composables/cloudStatus'

const file = { id: 's!f1', etag: '"v1"' } as unknown as Resource

describe('useBirdIndicator', () => {
  beforeEach(() => {
    post.mockReset()
    invalidateCloudStatus('s!f1')
  })

  it('draws nothing for a file Synaplan does not know', () => {
    post.mockResolvedValue({ data: { enabled: true, items: [] } })
    expect(useBirdIndicator().getResourceIndicators(file)).toBeUndefined()
    cloudMarks['s!f1'] = { state: 'none', folder: '', fileId: 0 }
    expect(useBirdIndicator().getResourceIndicators(file)).toBeUndefined()
  })

  it.each([
    ['searchable', 'Searchable by AI'],
    ['adding', 'Being made searchable.'],
    ['updating', 'Changed, updating.'],
    ['changed', 'Changed since it was made searchable.'],
    ['failed', 'Synaplan could not make this file searchable.']
  ])('labels a %s file', (state, label) => {
    post.mockResolvedValue({ data: { enabled: true, items: [] } })
    cloudMarks['s!f1'] = { state, folder: 'Invoices', fileId: 9 }
    const [indicator] = useBirdIndicator().getResourceIndicators(file) || []
    expect(indicator).toMatchObject({
      id: 'synaplan-s!f1',
      label,
      accessibleDescription: label,
      icon: 'bird.svg',
      type: 'synaplan-searchable'
    })
  })

  it('asks Synaplan about the files in view in one request', async () => {
    post.mockResolvedValue({ data: { enabled: true, items: [] } })
    const bird = useBirdIndicator()
    bird.getResourceIndicators(file)
    bird.getResourceIndicators({ id: 's!f2', etag: '"v2"' } as unknown as Resource)
    await new Promise((resolve) => setTimeout(resolve, 60))
    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][1]).toEqual({
      items: [
        { id: 's!f1', version: '"v1"' },
        { id: 's!f2', version: '"v2"' }
      ]
    })
  })
})
