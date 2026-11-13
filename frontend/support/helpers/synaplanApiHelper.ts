import { APIRequestContext } from '@playwright/test'

/**
 * Direct calls to Synaplan's HTTP API, bypassing the synaplan-opencloud
 * backend. Used by E2E tests that need to clean up their own fixtures
 * (e.g. wipe an existing knowledge group before exercising the
 * "create new group" flow). Pair with getAccessToken({ clientId:
 * 'synaplan-app', clientSecret: 'test-oidc-secret' }) to obtain a
 * Synaplan-scoped Bearer token.
 */

interface SynaplanFile {
  id: number
}

interface SynaplanFilesListResponse {
  success?: boolean
  files?: SynaplanFile[]
}

/**
 * Delete every file currently registered in the given group via
 * Synaplan's /api/v1/files endpoints. No-ops if the group is empty
 * or doesn't exist. Use this before tests that exercise the
 * "Make searchable by AI" flow with a fixed group name to keep the
 * Synaplan database clean across runs.
 */
export async function wipeKnowledgeGroup(
  request: APIRequestContext,
  baseUrl: string,
  token: string,
  groupKey: string
): Promise<void> {
  const headers = { Authorization: `Bearer ${token}` }
  const trimmedBase = baseUrl.replace(/\/+$/, '')

  const listResp = await request.get(`${trimmedBase}/api/v1/files`, {
    params: { group_key: groupKey },
    headers
  })
  if (!listResp.ok()) {
    throw new Error(`synaplan files list failed: ${listResp.status()} ${await listResp.text()}`)
  }

  const body = (await listResp.json()) as SynaplanFilesListResponse
  const files = body.files ?? []

  for (const file of files) {
    const delResp = await request.delete(`${trimmedBase}/api/v1/files/${file.id}`, { headers })
    if (!delResp.ok()) {
      throw new Error(
        `synaplan delete file ${file.id} failed: ${delResp.status()} ${await delResp.text()}`
      )
    }
  }
}

/**
 * Upload a text file straight into the person's Synaplan files, as a
 * file that was made in Synaplan. Returns its Synaplan file id.
 */
export async function uploadToSynaplan(
  request: APIRequestContext,
  baseUrl: string,
  token: string,
  name: string,
  content: string
): Promise<number> {
  const resp = await request.post(`${baseUrl.replace(/\/+$/, '')}/api/v1/files/upload`, {
    headers: { Authorization: `Bearer ${token}` },
    multipart: {
      'files[]': { name, mimeType: 'text/plain', buffer: Buffer.from(content) },
      process_level: 'extract'
    }
  })
  if (!resp.ok()) {
    throw new Error(`synaplan upload failed: ${resp.status()} ${await resp.text()}`)
  }
  const body = (await resp.json()) as { files?: { id: number }[] }
  const id = body.files?.[0]?.id
  if (!id) throw new Error('synaplan upload returned no file id')
  return id
}

export async function deleteSynaplanFileQuiet(
  request: APIRequestContext,
  baseUrl: string,
  token: string,
  fileId: number
): Promise<void> {
  await request
    .delete(`${baseUrl.replace(/\/+$/, '')}/api/v1/files/${fileId}`, {
      headers: { Authorization: `Bearer ${token}` }
    })
    .catch(() => {})
}

interface SynaplanModelRow {
  id: number
  name: string
  service: string
}

/**
 * The chat model Synaplan uses for the person when they pick none, as
 * the chat dialog labels it ("name (service)"). A journey that picks a
 * model picks this one: it works in the dev stack with real providers
 * and in the CI test stack, where only the stub answers.
 */
export async function defaultChatModelLabel(
  request: APIRequestContext,
  baseUrl: string,
  token: string
): Promise<string> {
  const headers = { Authorization: `Bearer ${token}` }
  const trimmedBase = baseUrl.replace(/\/+$/, '')

  const defaultsResp = await request.get(`${trimmedBase}/api/v1/config/models/defaults`, {
    headers
  })
  if (!defaultsResp.ok()) {
    throw new Error(`synaplan model defaults failed: ${defaultsResp.status()}`)
  }
  const defaults = (await defaultsResp.json()) as { defaults?: { CHAT?: number | string } }
  const chatId = Number(defaults.defaults?.CHAT)

  const modelsResp = await request.get(`${trimmedBase}/api/v1/config/models`, { headers })
  if (!modelsResp.ok()) {
    throw new Error(`synaplan models list failed: ${modelsResp.status()}`)
  }
  const models = (await modelsResp.json()) as { models?: { CHAT?: SynaplanModelRow[] } }
  const row = models.models?.CHAT?.find((m) => m.id === chatId)
  if (!row) {
    throw new Error(`synaplan default chat model ${chatId} is not in the model list`)
  }
  return row.service ? `${row.name} (${row.service})` : row.name
}
