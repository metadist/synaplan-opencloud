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
