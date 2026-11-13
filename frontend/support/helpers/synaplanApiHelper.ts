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
  filename?: string
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
 * The names of the files Synaplan keeps in a knowledge group, via its
 * /api/v1/files listing.
 */
export async function knowledgeFileNames(
  request: APIRequestContext,
  baseUrl: string,
  token: string,
  groupKey: string
): Promise<string[]> {
  const resp = await request.get(`${baseUrl.replace(/\/+$/, '')}/api/v1/files`, {
    params: { group_key: groupKey },
    headers: { Authorization: `Bearer ${token}` }
  })
  if (!resp.ok()) {
    throw new Error(`synaplan files list failed: ${resp.status()} ${await resp.text()}`)
  }
  const body = (await resp.json()) as SynaplanFilesListResponse
  return (body.files ?? []).map((file) => file.filename ?? '')
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

/**
 * The id of the person's active link to an OpenCloud, as Synaplan lists
 * it, or 0 when there is none yet. Synaplan links a person on their first
 * call through this OpenCloud.
 */
export async function openCloudLinkId(
  request: APIRequestContext,
  baseUrl: string,
  token: string
): Promise<number> {
  const resp = await request.get(`${baseUrl.replace(/\/+$/, '')}/api/v1/me/platform-links`, {
    headers: { Authorization: `Bearer ${token}` }
  })
  if (!resp.ok()) {
    throw new Error(`synaplan links failed: ${resp.status()} ${await resp.text()}`)
  }
  const body = (await resp.json()) as {
    links?: { id: number; client: string; status: string }[]
  }
  return body.links?.find((l) => l.client === 'opencloud' && l.status === 'active')?.id ?? 0
}

/** Ask Synaplan to copy one of its files into the linked OpenCloud. */
export async function sendToOpenCloud(
  request: APIRequestContext,
  baseUrl: string,
  token: string,
  fileId: number,
  linkId: number
): Promise<{ status: number; state: string; reference: string }> {
  const resp = await request.post(`${baseUrl.replace(/\/+$/, '')}/api/v1/files/${fileId}/send`, {
    headers: { Authorization: `Bearer ${token}` },
    data: { destination: 'platform', identity_id: linkId }
  })
  const body = (await resp.json().catch(() => ({}))) as {
    reference?: string | null
    context?: { state?: string }
  }
  return {
    status: resp.status(),
    state: body.context?.state ?? '',
    reference: body.reference ?? ''
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
