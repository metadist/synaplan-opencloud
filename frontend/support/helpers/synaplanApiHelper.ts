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
 * "Add to Knowledge" flow with a fixed group name to keep the
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

/** Upload a text file to the person's Synaplan files; returns its id. */
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

/** The person's link to the OpenCloud at host, as Synaplan lists it. */
export async function linkToCloud(
  request: APIRequestContext,
  baseUrl: string,
  token: string,
  host: string
): Promise<number> {
  const resp = await request.get(`${baseUrl.replace(/\/+$/, '')}/api/v1/me/platform-links`, {
    headers: { Authorization: `Bearer ${token}` }
  })
  if (!resp.ok()) {
    throw new Error(`synaplan links failed: ${resp.status()} ${await resp.text()}`)
  }
  const body = (await resp.json()) as {
    links?: { id: number; client: string; host: string; status: string }[]
  }
  const link = body.links?.find(
    (l) => l.client === 'opencloud' && l.host === host && l.status === 'active'
  )
  if (!link) throw new Error(`no active link to ${host}`)
  return link.id
}

/** Ask Synaplan to copy one of its files into the linked cloud. */
export async function sendToCloud(
  request: APIRequestContext,
  baseUrl: string,
  token: string,
  fileId: number,
  linkId: number
): Promise<{ status: number; reference: string | null; state: string }> {
  const resp = await request.post(`${baseUrl.replace(/\/+$/, '')}/api/v1/files/${fileId}/send`, {
    headers: { Authorization: `Bearer ${token}` },
    data: { destination: 'platform', identity_id: linkId }
  })
  const body = (await resp.json()) as { reference?: string | null; context?: { state?: string } }
  return {
    status: resp.status(),
    reference: body.reference ?? null,
    state: body.context?.state ?? ''
  }
}

/** Delete a file from the person's Synaplan files; ignores failures. */
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

export interface SynaplanGroupFile {
  id: number
  filename: string
  source: string | null
  file_size: number
  text_preview?: string | null
}

/** The files in one of the person's knowledge groups. */
export async function filesInGroup(
  request: APIRequestContext,
  baseUrl: string,
  token: string,
  groupKey: string
): Promise<SynaplanGroupFile[]> {
  const resp = await request.get(`${baseUrl.replace(/\/+$/, '')}/api/v1/files`, {
    params: { group_key: groupKey },
    headers: { Authorization: `Bearer ${token}` }
  })
  if (!resp.ok()) {
    throw new Error(`synaplan files list failed: ${resp.status()} ${await resp.text()}`)
  }
  return ((await resp.json()) as { files?: SynaplanGroupFile[] }).files ?? []
}
