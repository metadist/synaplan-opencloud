import { test, expect } from '@playwright/test'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import { allowBackgroundWork, stopBackgroundWork } from '../../support/helpers/cloudHelper'
import {
  deleteFileQuiet,
  downloadTextFile,
  getPersonalSpace
} from '../../support/helpers/filesHelper'
import {
  deleteSynaplanFileQuiet,
  linkToCloud,
  sendToCloud,
  uploadToSynaplan
} from '../../support/helpers/synaplanApiHelper'

/**
 * Copy to cloud: Synaplan sends one of the person's files to their
 * OpenCloud. Synaplan only notifies; the service claims and downloads the
 * copy with the person's own Synaplan token and writes it over WebDAV with
 * the person's own OpenCloud token, both minted from the offline token the
 * person allowed. It works with nobody signed in, and stops when the person
 * stops background work.
 */

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const SYNAPLAN_API_URL = process.env.SYNAPLAN_API_URL ?? 'http://localhost:8000'
const OC_HOST = new URL(OC_API_URL).host

test.describe.configure({ mode: 'serial' })

let synaplanToken: string
let ocToken: string
let webDavUrl: string
const synaplanFiles: number[] = []
const cloudFiles: string[] = []

test.beforeEach(async ({ request }) => {
  synaplanToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD, {
    clientId: 'synaplan-app',
    clientSecret: 'test-oidc-secret'
  })
  ocToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  webDavUrl = (await getPersonalSpace(request, OC_API_URL, ocToken)).webDavUrl
})

test.afterEach(async ({ request }) => {
  for (const id of synaplanFiles.splice(0)) {
    await deleteSynaplanFileQuiet(request, SYNAPLAN_API_URL, synaplanToken, id)
  }
  for (const path of cloudFiles.splice(0)) {
    await deleteFileQuiet(request, webDavUrl, ocToken, path)
  }
})

async function sendNewFile(request: Parameters<typeof uploadToSynaplan>[0], name: string) {
  const fileId = await uploadToSynaplan(
    request,
    SYNAPLAN_API_URL,
    synaplanToken,
    name,
    `copy of ${name}`
  )
  synaplanFiles.push(fileId)
  const linkId = await linkToCloud(request, SYNAPLAN_API_URL, synaplanToken, OC_HOST)
  return sendToCloud(request, SYNAPLAN_API_URL, synaplanToken, fileId, linkId)
}

test('a copy from Synaplan lands in the personal space with nobody signed in', async ({
  browser,
  request
}) => {
  test.setTimeout(90_000)
  const { page } = await loginAsUser(browser, TEST_USER, TEST_PASSWORD)
  await allowBackgroundWork(page)
  await logout(page)

  const name = `copy-${Date.now()}.txt`
  const sent = await sendNewFile(request, name)
  expect(sent.status, JSON.stringify(sent)).toBe(200)
  expect(sent.state).toBe('delivered')
  expect(sent.reference).toBe(`/Synaplan/Documents/${name}`)
  cloudFiles.push(`Synaplan/Documents/${name}`)

  expect(await downloadTextFile(request, webDavUrl, ocToken, `Synaplan/Documents/${name}`)).toBe(
    `copy of ${name}`
  )
})

test('after stop, Synaplan cannot place a copy', async ({ browser, request }) => {
  test.setTimeout(90_000)
  const { page } = await loginAsUser(browser, TEST_USER, TEST_PASSWORD)
  await stopBackgroundWork(page)

  const name = `copy-stopped-${Date.now()}.txt`
  const sent = await sendNewFile(request, name)
  expect(sent.state).not.toBe('delivered')
  const probe = await request.get(`${webDavUrl}/Synaplan/Documents/${name}`, {
    headers: { Authorization: `Bearer ${ocToken}` }
  })
  expect(probe.status()).toBe(404)

  await allowBackgroundWork(page)
  await logout(page)
})
