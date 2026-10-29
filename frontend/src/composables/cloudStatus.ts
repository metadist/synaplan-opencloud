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
const pending = new Map<string, Item>()
const queuedVersion = new Map<string, string>()
let timer: ReturnType<typeof setTimeout> | undefined
let client:
  { post: (url: string, body: unknown, opts: unknown) => Promise<{ data: unknown }> } | undefined

export function markFor(id: string): CloudMark | undefined {
  return cloudMarks[id]
}

export function queueCloudStatus(
  http: { post: (url: string, body: unknown, opts: unknown) => Promise<{ data: unknown }> },
  item: Item
) {
  if (queuedVersion.get(item.id) === item.version && cloudMarks[item.id]) return
  queuedVersion.set(item.id, item.version)
  client = http
  pending.set(item.id, item)
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => {
    const batch = [...pending.values()]
    pending.clear()
    if (client && batch.length) void loadCloudStatus(client, batch)
  }, 40)
}

export async function loadCloudStatus(
  http: { post: (url: string, body: unknown, opts: unknown) => Promise<{ data: unknown }> },
  items: Item[]
) {
  if (!items.length) return
  const res = await http.post('/api/synaplan/cloud/status', { items }, { schema: statusSchema })
  const data = statusSchema.parse(res.data)
  const watched = new Set(
    data.watches.map((watch) => watch.target_id).filter((id): id is string => !!id)
  )
  for (const item of data.items) {
    cloudMarks[item.id] = {
      state: item.state,
      folder: item.knowledge_folder?.name ?? '',
      fileId: item.file_id ?? 0,
      watched: watched.has(item.id)
    }
  }
}
