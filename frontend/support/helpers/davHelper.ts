import { APIRequestContext } from '@playwright/test'

/** The OpenCloud resource id of a path in the personal space, over WebDAV. */
export async function resourceId(
  request: APIRequestContext,
  webDavUrl: string,
  accessToken: string,
  path: string
): Promise<string> {
  const url = `${webDavUrl.replace(/\/$/, '')}/${path.split('/').map(encodeURIComponent).join('/')}`
  const resp = await request.fetch(url, {
    method: 'PROPFIND',
    headers: { Authorization: `Bearer ${accessToken}`, Depth: '0', 'Content-Type': 'application/xml' },
    data: '<?xml version="1.0"?><d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:prop><oc:fileid/></d:prop></d:propfind>'
  })
  const match = /<oc:fileid>([^<]+)<\/oc:fileid>/.exec(await resp.text())
  if (!match) throw new Error(`no resource id for ${path}: ${resp.status()}`)
  return match[1]
}
