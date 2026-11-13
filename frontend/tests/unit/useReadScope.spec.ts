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

  it.each([
    ['watched', 'allowed'],
    ['allowed', 'allowed'],
    ['all', 'all']
  ])('allowing a folder in %s mode saves %s mode', async (current, expected) => {
    get.mockResolvedValueOnce({
      data: {
        available: true,
        readScope: {
          mode: current,
          folders: [
            { id: 'f1', path: 'Old' },
            { id: 'f2', path: 'B' }
          ]
        }
      }
    })
    put.mockImplementationOnce((_url, body) =>
      Promise.resolve({ data: { available: true, readScope: body } })
    )
    await useReadScope().allowFolder({ id: 'f1', path: 'New' })
    expect(put.mock.calls[0][1]).toEqual({
      mode: expected,
      folders: [
        { id: 'f2', path: 'B' },
        { id: 'f1', path: 'New' }
      ]
    })
  })
})
