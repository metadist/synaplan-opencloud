import { describe, it, expect, vi, beforeEach } from 'vitest'

const get = vi.fn()
const put = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { get, put } })
}))

import { useReadScope } from '../../src/composables/useReadScope'

describe('useReadScope', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
  })

  it('reads how this OpenCloud signs in to Synaplan', async () => {
    get.mockResolvedValueOnce({ data: { login: 'person' } })
    await expect(useReadScope().login()).resolves.toBe('person')
    expect(get.mock.calls[0][0]).toBe('/api/synaplan/connection')
  })

  it('reads the setting', async () => {
    const answer = {
      available: true,
      readScope: { mode: 'watched', folders: [] as { id: string; path: string }[] }
    }
    get.mockResolvedValueOnce({ data: answer })
    await expect(useReadScope().load()).resolves.toEqual(answer)
    expect(get.mock.calls[0][0]).toBe('/api/synaplan/preferences')
  })

  it('saves the setting and answers what the service kept', async () => {
    const kept = { mode: 'allowed', folders: [{ id: 'f1', path: 'Specs' }] }
    put.mockResolvedValueOnce({ data: { available: true, readScope: kept } })
    const saved = await useReadScope().save({
      mode: 'allowed',
      folders: [{ id: 'f1', path: 'whatever' }]
    })
    expect(saved).toEqual(kept)
    expect(put.mock.calls[0][0]).toBe('/api/synaplan/preferences')
  })
})
