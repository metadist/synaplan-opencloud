import { APIRequestContext } from '@playwright/test'
import { readFileSync } from 'fs'

/**
 * API-driven file fixture helpers for E2E tests. Use these to seed
 * files directly against OpenCloud's graph + WebDAV APIs instead of
 * driving the UI upload flow — setup is one order of magnitude
 * faster and is decoupled from file-browser markup.
 *
 * Every helper takes an OIDC access token (obtain one via
 * `getAccessToken` from authHelper).
 */

/**
 * Look up the authenticated user's personal storage space via the
 * Graph API. Returns the space id plus the absolute webDavUrl under
 * which we PUT / DELETE files on that space.
 */
export async function getPersonalSpace(
  request: APIRequestContext,
  baseUrl: string,
  accessToken: string
): Promise<{ id: string; webDavUrl: string }> {
  const resp = await request.get(`${baseUrl}/graph/v1.0/me/drives`, {
    headers: { Authorization: `Bearer ${accessToken}` }
  })
  if (!resp.ok()) {
    throw new Error(`graph /me/drives failed: ${resp.status()} ${await resp.text()}`)
  }
  const body = (await resp.json()) as {
    value: Array<{ id: string; driveType: string; root?: { webDavUrl?: string } }>
  }
  const personal = body.value.find((d) => d.driveType === 'personal')
  if (!personal) {
    throw new Error('no personal drive found')
  }
  const webDavUrl = personal.root?.webDavUrl
  if (!webDavUrl) {
    throw new Error('personal drive has no webDavUrl')
  }
  return { id: personal.id, webDavUrl }
}

/**
 * Upload raw bytes to the given WebDAV base URL (normally the
 * `webDavUrl` returned by getPersonalSpace).
 */
export async function uploadFile(
  request: APIRequestContext,
  webDavUrl: string,
  accessToken: string,
  fileName: string,
  content: string | Buffer,
  contentType: string
): Promise<void> {
  const url = buildFileUrl(webDavUrl, fileName)
  const resp = await request.put(url, {
    headers: {
      Authorization: `Bearer ${accessToken}`,
      'Content-Type': contentType
    },
    data: content
  })
  if (!resp.ok()) {
    throw new Error(`PUT ${url} failed: ${resp.status()} ${await resp.text()}`)
  }
}

/**
 * Convenience wrapper for the common "PUT a text file" case.
 */
export async function uploadTextFile(
  request: APIRequestContext,
  webDavUrl: string,
  accessToken: string,
  fileName: string,
  content: string
): Promise<void> {
  return uploadFile(request, webDavUrl, accessToken, fileName, content, 'text/plain')
}

/**
 * Read a binary fixture from disk (relative to the frontend root) and
 * upload it. Used by tests that need to exercise Synaplan's binary
 * extraction pipeline (PDF / DOCX / ODF).
 */
export async function uploadFileFromDisk(
  request: APIRequestContext,
  webDavUrl: string,
  accessToken: string,
  fileName: string,
  diskPath: string,
  contentType: string
): Promise<void> {
  const bytes = readFileSync(diskPath)
  return uploadFile(request, webDavUrl, accessToken, fileName, bytes, contentType)
}

/**
 * Delete a previously-uploaded file. Best-effort — swallows errors so
 * test teardown stays non-fatal if the file was already cleaned up.
 */
export async function deleteFileQuiet(
  request: APIRequestContext,
  webDavUrl: string,
  accessToken: string,
  fileName: string
): Promise<void> {
  const url = buildFileUrl(webDavUrl, fileName)
  try {
    await request.delete(url, {
      headers: { Authorization: `Bearer ${accessToken}` }
    })
  } catch {
    // ignore — teardown must not fail the test
  }
}

/**
 * Idempotently create a folder via WebDAV MKCOL. Returns silently if
 * the folder already exists (405 Method Not Allowed is the canonical
 * "exists" response). Used so test fixture files don't clutter the
 * user's personal space — group them under a dedicated subdir.
 */
export async function ensureFolder(
  request: APIRequestContext,
  webDavUrl: string,
  accessToken: string,
  folderName: string
): Promise<void> {
  const url = buildFileUrl(webDavUrl, folderName)
  const resp = await request.fetch(url, {
    method: 'MKCOL',
    headers: { Authorization: `Bearer ${accessToken}` }
  })
  // 201 Created = new folder, 405 Method Not Allowed = already exists.
  // Both are fine; anything else is a genuine failure.
  if (resp.status() !== 201 && resp.status() !== 405) {
    throw new Error(`MKCOL ${url} failed: ${resp.status()} ${await resp.text()}`)
  }
}

function buildFileUrl(webDavUrl: string, filePath: string): string {
  // Encode each path segment individually so `/` stays a separator
  // while the names themselves get properly percent-encoded.
  const encoded = filePath.split('/').map(encodeURIComponent).join('/')
  return `${webDavUrl.replace(/\/$/, '')}/${encoded}`
}
