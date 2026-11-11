import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  IN_PROGRESS_TTL_MS,
  SETTLED_TTL_MS,
  cloudFilesEnabled,
  cloudMarks,
  invalidateCloudStatus,
  loadCloudStatus,
  markFor,
  queueCloudStatus
} from '../../src/composables/cloudStatus'

function answer(state: string) {
  return {
    data: {
      enabled: true,
      items: [{ id: 'f1', state, file_id: 9, knowledge_folder: { name: 'Invoices' } }],
      watches: [] as { target_id?: string }[]
    }
  }
}

describe('cloudStatus cache', () => {
  beforeEach(() => {
    for (const key of Object.keys(cloudMarks)) delete cloudMarks[key]
    cloudFilesEnabled.value = false
    invalidateCloudStatus('f1')
    vi.useRealTimers()
  })

  it('asks again after the settled ttl for the same version', async () => {
    const post = vi.fn().mockResolvedValue(answer('searchable'))
    await loadCloudStatus({ post }, [{ id: 'f1', version: 'v1' }], 1_000)
    expect(markFor('f1')?.state).toBe('searchable')

    queueCloudStatus({ post }, { id: 'f1', version: 'v1' }, 1_000 + SETTLED_TTL_MS - 1)
    queueCloudStatus({ post }, { id: 'f1', version: 'v1' }, 1_000 + SETTLED_TTL_MS + 1)
    await new Promise((resolve) => setTimeout(resolve, 60))
    expect(post).toHaveBeenCalledTimes(2)
  })

  it('polls a state that is still moving sooner', async () => {
    const post = vi.fn().mockResolvedValue(answer('adding'))
    await loadCloudStatus({ post }, [{ id: 'f1', version: 'v1' }], 1_000)
    queueCloudStatus({ post }, { id: 'f1', version: 'v1' }, 1_000 + IN_PROGRESS_TTL_MS + 1)
    await new Promise((resolve) => setTimeout(resolve, 60))
    expect(post).toHaveBeenCalledTimes(2)
  })

  it('forgets a file on invalidate', async () => {
    const post = vi.fn().mockResolvedValue(answer('searchable'))
    await loadCloudStatus({ post }, [{ id: 'f1', version: 'v1' }], 1_000)
    invalidateCloudStatus('f1')
    expect(markFor('f1')).toBeUndefined()
    queueCloudStatus({ post }, { id: 'f1', version: 'v1' }, 1_001)
    await new Promise((resolve) => setTimeout(resolve, 60))
    expect(post).toHaveBeenCalledTimes(2)
  })

  it('keeps the newer mark when an older answer arrives last', async () => {
    let release: (value: { data: unknown }) => void = () => {}
    const slow = new Promise<{ data: unknown }>((resolve) => {
      release = resolve
    })
    const post = vi
      .fn()
      .mockImplementationOnce(() => slow)
      .mockResolvedValueOnce(answer('changed'))
    const first = loadCloudStatus({ post }, [{ id: 'f1', version: 'v1' }], 1_000)
    await loadCloudStatus({ post }, [{ id: 'f1', version: 'v2' }], 2_000)
    expect(markFor('f1')?.state).toBe('changed')
    expect(cloudFilesEnabled.value).toBe(true)
    release({
      data: {
        enabled: false,
        items: [{ id: 'f1', state: 'searchable', file_id: 9, knowledge_folder: null }]
      }
    })
    await first
    expect(markFor('f1')?.state).toBe('changed')
    expect(cloudFilesEnabled.value).toBe(true)

    queueCloudStatus({ post }, { id: 'f1', version: 'v2' }, 3_000)
    await new Promise((resolve) => setTimeout(resolve, 60))
    expect(post).toHaveBeenCalledTimes(2)
  })

  it('drops cached marks when cloud file state turns off', async () => {
    const post = vi
      .fn()
      .mockResolvedValueOnce(answer('searchable'))
      .mockResolvedValueOnce({ data: { enabled: false, items: [] } })
    await loadCloudStatus({ post }, [{ id: 'f1', version: 'v1' }], 1_000)
    expect(markFor('f1')?.state).toBe('searchable')
    await loadCloudStatus({ post }, [{ id: 'f1', version: 'v2' }], 2_000)
    expect(cloudFilesEnabled.value).toBe(false)
    expect(markFor('f1')).toBeUndefined()
  })

  it('records cloud file state only when the answer says it is on', async () => {
    const post = vi.fn().mockResolvedValue({ data: { enabled: false, items: [] } })
    await loadCloudStatus({ post }, [{ id: 'f1', version: 'v1' }], 1_000)
    expect(cloudFilesEnabled.value).toBe(false)

    post.mockResolvedValue(answer('searchable'))
    await loadCloudStatus({ post }, [{ id: 'f1', version: 'v2' }], 2_000)
    expect(cloudFilesEnabled.value).toBe(true)
  })

  it('leaves cloud file state on when a later answer is unreachable', async () => {
    const post = vi
      .fn()
      .mockResolvedValueOnce(answer('searchable'))
      .mockResolvedValueOnce({ data: { enabled: true, unreachable: true, items: [] } })
    await loadCloudStatus({ post }, [{ id: 'f1', version: 'v1' }], 1_000)
    expect(cloudFilesEnabled.value).toBe(true)
    await loadCloudStatus({ post }, [{ id: 'f1', version: 'v2' }], 2_000)
    expect(cloudFilesEnabled.value).toBe(true)
    expect(markFor('f1')?.state).toBe('searchable')
  })

  it('treats an unreachable answer like a failed one', async () => {
    const post = vi
      .fn()
      .mockResolvedValueOnce({ data: { enabled: true, unreachable: true, items: [], watches: [] } })
      .mockResolvedValue(answer('searchable'))
    await loadCloudStatus({ post }, [{ id: 'f1', version: 'v1' }], 1_000)
    expect(markFor('f1')).toBeUndefined()
    queueCloudStatus({ post }, { id: 'f1', version: 'v1' }, 1_001)
    await new Promise((resolve) => setTimeout(resolve, 60))
    expect(post).toHaveBeenCalledTimes(2)
    expect(markFor('f1')?.state).toBe('searchable')
  })

  it('asks again on its own while a state is still moving', async () => {
    vi.useFakeTimers()
    const post = vi
      .fn()
      .mockResolvedValueOnce(answer('adding'))
      .mockResolvedValue(answer('searchable'))
    await loadCloudStatus({ post }, [{ id: 'f1', version: 'v1' }], 1_000)
    expect(markFor('f1')?.state).toBe('adding')
    await vi.advanceTimersByTimeAsync(IN_PROGRESS_TTL_MS + 1)
    expect(post).toHaveBeenCalledTimes(2)
    expect(markFor('f1')?.state).toBe('searchable')
    vi.useRealTimers()
  })

  it('does not keep a failed answer as fresh', async () => {
    const post = vi
      .fn()
      .mockRejectedValueOnce(new Error('down'))
      .mockResolvedValue(answer('searchable'))
    await loadCloudStatus({ post }, [{ id: 'f1', version: 'v1' }], 1_000)
    expect(markFor('f1')).toBeUndefined()
    queueCloudStatus({ post }, { id: 'f1', version: 'v1' }, 1_001)
    await new Promise((resolve) => setTimeout(resolve, 60))
    expect(post).toHaveBeenCalledTimes(2)
    expect(markFor('f1')?.state).toBe('searchable')
  })
})
