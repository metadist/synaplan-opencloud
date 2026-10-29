import { reactive } from 'vue'
import { z } from 'zod'

export type CloudMark = {
  state: string
  folder: string
  fileId: number
  watched: boolean
}

export const cloudMarks = reactive<Record<string, CloudMark>>({})

const statusSchema = z.object({
  enabled: z.boolean().optional(),
  unreachable: z.boolean().optional(),
  items: z
    .array(
      z.object({
        id: z.string(),
        state: z.string(),
        file_id: z.number().nullable().optional(),
        knowledge_folder: z.object({ name: z.string() }).nullable().optional()
      })
    )
    .optional()
    .default([]),
  watches: z
    .array(
      z.object({
        target_id: z.string().optional(),
        id: z.union([z.string(), z.number()]).optional()
      })
    )
    .optional()
    .default([])
})

type Item = { id: string; version: string }
type Http = { post: (url: string, body: unknown, opts: unknown) => Promise<{ data: unknown }> }

/** A settled state is trusted this long before the folder is asked again. */
export const SETTLED_TTL_MS = 60_000
/** A state that is still moving in Synaplan is asked again sooner. */
export const IN_PROGRESS_TTL_MS = 10_000

const IN_PROGRESS = new Set(['adding', 'updating'])

const pending = new Map<string, Item>()
const fetchedAt = new Map<string, { version: string; at: number }>()
let timer: ReturnType<typeof setTimeout> | undefined
let client: Http | undefined

export function markFor(id: string): CloudMark | undefined {
  return cloudMarks[id]
}

/** Forget what is known about a file, so the next look asks Synaplan. */
export function invalidateCloudStatus(id: string) {
  fetchedAt.delete(id)
  delete cloudMarks[id]
}

function isFresh(id: string, version: string, now: number): boolean {
  const seen = fetchedAt.get(id)
  if (!seen || seen.version !== version) return false
  const mark = cloudMarks[id]
  const ttl = mark && IN_PROGRESS.has(mark.state) ? IN_PROGRESS_TTL_MS : SETTLED_TTL_MS
  return now - seen.at < ttl
}

export function queueCloudStatus(http: Http, item: Item, now = Date.now()) {
  if (isFresh(item.id, item.version, now)) return
  client = http
  pending.set(item.id, item)
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => {
    const batch = [...pending.values()]
    pending.clear()
    if (client && batch.length) void loadCloudStatus(client, batch)
  }, 40)
}

export async function loadCloudStatus(http: Http, items: Item[], now = Date.now()) {
  if (!items.length) return
  for (const item of items) {
    fetchedAt.set(item.id, { version: item.version, at: now })
  }
  let data: z.infer<typeof statusSchema>
  try {
    const res = await http.post('/api/synaplan/cloud/status', { items }, { schema: statusSchema })
    data = statusSchema.parse(res.data)
    if (data.unreachable) throw new Error('synaplan unreachable')
  } catch {
    // Ask again on the next look instead of showing a stale bird.
    for (const item of items) fetchedAt.delete(item.id)
    return
  }
  const watched = new Set(
    data.watches.map((watch) => watch.target_id).filter((id): id is string => !!id)
  )
  const moving: Item[] = []
  for (const item of data.items) {
    cloudMarks[item.id] = {
      state: item.state,
      folder: item.knowledge_folder?.name ?? '',
      fileId: item.file_id ?? 0,
      watched: watched.has(item.id)
    }
    if (IN_PROGRESS.has(item.state)) {
      const asked = items.find((requested) => requested.id === item.id)
      if (asked) moving.push(asked)
    }
  }
  scheduleFollowUp(http, moving)
}

let followUp: ReturnType<typeof setTimeout> | undefined

/**
 * A file that Synaplan is still adding or updating is asked about again
 * after its TTL, so an idle view reaches the settled state on its own.
 * Nothing renders twice: the next answer only replaces the mark.
 */
function scheduleFollowUp(http: Http, moving: Item[]) {
  if (followUp) clearTimeout(followUp)
  followUp = undefined
  if (!moving.length) return
  followUp = setTimeout(() => {
    followUp = undefined
    const still = moving.filter((item) => IN_PROGRESS.has(cloudMarks[item.id]?.state ?? ''))
    for (const item of still) fetchedAt.delete(item.id)
    if (still.length) void loadCloudStatus(http, still)
  }, IN_PROGRESS_TTL_MS)
}
