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

/**
 * Store a file in Synaplan without processing it and return its id. Used
 * by journeys that need a file Synaplan holds, such as an image the test
 * stack's model cannot make.
 */
export async function storeSynaplanFile(
  request: APIRequestContext,
  baseUrl: string,
  token: string,
  name: string,
  content: Buffer,
  mimeType: string
): Promise<number> {
  const resp = await request.post(`${baseUrl.replace(/\/+$/, '')}/api/v1/files/upload`, {
    headers: { Authorization: `Bearer ${token}` },
    multipart: {
      'files[]': { name, mimeType, buffer: content },
      process_level: 'store'
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

interface SynaplanListedFile {
  id: number
  path?: string
}

/**
 * Resolve a generated file URL (/api/v1/files/{id}/download or
 * /api/v1/files/uploads/...) to its Synaplan file ID so E2E journeys
 * can clean it up regardless of whether saving to OpenCloud succeeded.
 * Returns 0 when fileUrl does not name a stored Synaplan file (e.g. an
 * external placeholder URL in test stacks).
 */
export async function synaplanFileIdForUrl(
  request: APIRequestContext,
  baseUrl: string,
  token: string,
  fileUrl: string
): Promise<number> {
  const downloadId = synaplanDownloadId(fileUrl)
  if (downloadId) return downloadId

  const relative = synaplanUploadPath(fileUrl)
  if (!relative) return 0

  const name = relative.split('/').pop() ?? relative
  const resp = await request.get(`${baseUrl.replace(/\/+$/, '')}/api/v1/files`, {
    headers: { Authorization: `Bearer ${token}` },
    params: { search: name, limit: 50, sort: 'date_desc' }
  })
  if (!resp.ok()) {
    throw new Error(`synaplan files list failed: ${resp.status()} ${await resp.text()}`)
  }
  const body = (await resp.json()) as { files?: SynaplanListedFile[] }
  const match = (body.files ?? []).find((file) => synaplanListedPath(file.path ?? '') === relative)
  if (!match?.id) throw new Error(`synaplan file not found for ${fileUrl}`)
  return match.id
}

function synaplanDownloadId(fileUrl: string): number | undefined {
  const path = cleanUrlPath(fileUrl)
  if (!path) return undefined
  const match = /\/api\/v1\/files\/(\d+)\/download$/.exec(path)
  if (!match) return undefined
  const id = Number(match[1])
  return id > 0 ? id : undefined
}

function synaplanUploadPath(fileUrl: string): string | undefined {
  const path = cleanUrlPath(fileUrl)
  if (!path) return undefined
  const marker = '/api/v1/files/uploads/'
  const at = path.indexOf(marker)
  if (at < 0) return undefined
  const raw = path.slice(at + marker.length)
  if (!raw || raw.includes('..')) return undefined
  try {
    return decodeURIComponent(raw)
  } catch {
    return raw
  }
}

function synaplanListedPath(storedPath: string): string {
  const fromUrl = synaplanUploadPath(storedPath)
  if (fromUrl) return fromUrl
  return storedPath.replace(/^\/+/, '')
}

function cleanUrlPath(raw: string): string | undefined {
  try {
    const parsed = new URL(raw, 'https://synaplan.local')
    return parsed.pathname
  } catch {
    return undefined
  }
}
