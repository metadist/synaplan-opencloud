import { test, expect, Page } from '@playwright/test'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import { allowWorkWhileAway, stopWorkWhileAway } from '../../support/helpers/delegationHelper'
import {
  deleteFileQuiet,
  downloadTextFile,
  getPersonalSpace
} from '../../support/helpers/filesHelper'
import {
  deleteSynaplanFileQuiet,
  openCloudLinkId,
  sendToOpenCloud,
  uploadToSynaplan
} from '../../support/helpers/synaplanApiHelper'

/**
 * A copy from Synaplan: Synaplan only notifies this OpenCloud that it has a
 * file for the person. The service claims and downloads the copy with the
 * person's own Synaplan token, minted from the offline token they allowed,
 * and writes it with a reva token from OpenCloud's machine auth. It works
 * with nobody signed in, and stops when the person stops work while away.
 */

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const SYNAPLAN_API_URL = process.env.SYNAPLAN_API_URL ?? 'http://host.docker.internal:8000'

// Serial: both tests change whether the person allowed work while away.
test.describe.configure({ mode: 'serial' })

let accessToken: string
let synaplanToken: string
let webDavUrl: string
let userPage: Page
const synaplanFiles: number[] = []
const cloudFiles: string[] = []

test.beforeEach(async ({ browser, request }) => {
  accessToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  webDavUrl = (await getPersonalSpace(request, OC_API_URL, accessToken)).webDavUrl
  synaplanToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD, {
    clientId: 'synaplan-app',
    clientSecret: 'test-oidc-secret'
  })
  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
})

test.afterEach(async ({ request }) => {
  for (const id of synaplanFiles.splice(0)) {
    await deleteSynaplanFileQuiet(request, SYNAPLAN_API_URL, synaplanToken, id)
  }
  for (const path of cloudFiles.splice(0)) {
    await deleteFileQuiet(request, webDavUrl, accessToken, path)
  }
  await stopWorkWhileAway(userPage).catch(() => {})
  await logout(userPage).catch(() => {})
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
  // The Synaplan page called Synaplan through this OpenCloud, which links
  // the person there.
  let linkId = 0
  await expect
    .poll(async () => (linkId = await openCloudLinkId(request, SYNAPLAN_API_URL, synaplanToken)), {
      timeout: 15_000
    })
    .toBeGreaterThan(0)
  return sendToOpenCloud(request, SYNAPLAN_API_URL, synaplanToken, fileId, linkId)
}

test('a copy from Synaplan lands in the Synaplan folder with nobody signed in', async ({
  browser,
  request
}) => {
  test.setTimeout(90_000)
  await allowWorkWhileAway(userPage)
  await logout(userPage)

  const name = `delivery-${Date.now()}.txt`
  const path = `Synaplan/Documents/${name}`
  cloudFiles.push(path)
  const sent = await sendNewFile(request, name)
  expect(sent.status, JSON.stringify(sent)).toBeLessThan(300)

  await expect
    .poll(() => downloadTextFile(request, webDavUrl, accessToken, path).catch(() => ''), {
      timeout: 30_000
    })
    .toBe(`copy of ${name}`)

  // Signed in again, so afterEach can stop work while away.
  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
})

test('after stop, Synaplan cannot place a copy', async ({ request }) => {
  test.setTimeout(90_000)
  await stopWorkWhileAway(userPage)

  const name = `delivery-stopped-${Date.now()}.txt`
  const path = `Synaplan/Documents/${name}`
  cloudFiles.push(path)
  const sent = await sendNewFile(request, name)
  expect(sent.state).not.toBe('delivered')

  const probe = await request.get(`${webDavUrl}/${path}`, {
    headers: { Authorization: `Bearer ${accessToken}` }
  })
  expect(probe.status()).toBe(404)
})
