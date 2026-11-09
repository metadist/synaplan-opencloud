import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  IN_PROGRESS_TTL_MS,
  SETTLED_TTL_MS,
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
