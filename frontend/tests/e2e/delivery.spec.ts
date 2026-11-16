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
  listFileCopies,
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
let userPage: Page | undefined
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

test.afterEach(async ({ browser, request }) => {
  for (const id of synaplanFiles.splice(0)) {
    await deleteSynaplanFileQuiet(request, SYNAPLAN_API_URL, synaplanToken, id)
  }
  for (const path of cloudFiles.splice(0)) {
    await deleteFileQuiet(request, webDavUrl, accessToken, path)
  }
  // The signed-out journey logs out before it finishes. A failure before
  // the final login leaves that page closed, so cleanup signs in again
  // and still stops work while away.
  if (userPage === undefined || userPage.isClosed()) {
    userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
  }
  let revocationError: unknown
  try {
    await stopWorkWhileAway(userPage)
  } catch (error) {
    revocationError = error
  }
  if (!userPage.isClosed()) {
    try {
      await logout(userPage)
    } catch (error) {
      await userPage
        .context()
        .close()
        .catch(() => {})
      if (!revocationError) revocationError = error
    }
  }
  userPage = undefined
  if (revocationError) throw revocationError
})

function signedIn(): Page {
  if (!userPage) throw new Error('the test user is not signed in')
  return userPage
}

async function sendNewFile(request: Parameters<typeof uploadToSynaplan>[0], name: string) {
  const fileId = await uploadToSynaplan(
    request,
    SYNAPLAN_API_URL,
    synaplanToken,
    name,
    `copy of ${name}`,
    'store'
  )
  synaplanFiles.push(fileId)
  // Synaplan links a person on the first call made through this OpenCloud.
  // Opening the Synaplan page only talks to this service, so ensure the
  // link exists before polling for it.
  const linked = await request.get(`${OC_API_URL}/api/synaplan/me`, {
    headers: { Authorization: `Bearer ${accessToken}` }
  })
  expect(linked.ok(), await linked.text()).toBe(true)
  const me = (await linked.json()) as { status?: string }
  expect(me.status, JSON.stringify(me)).toBe('ok')

  let linkId = 0
  await expect
    .poll(async () => (linkId = await openCloudLinkId(request, SYNAPLAN_API_URL, synaplanToken)), {
      timeout: 15_000
    })
    .toBeGreaterThan(0)
  const sent = await sendToOpenCloud(request, SYNAPLAN_API_URL, synaplanToken, fileId, linkId)
  return { fileId, sent }
}

test('a copy from Synaplan lands in the Synaplan folder with nobody signed in', async ({
  browser,
  request
}) => {
  test.setTimeout(90_000)
  await allowWorkWhileAway(signedIn())
  await logout(signedIn())

  const name = `delivery-${Date.now()}.txt`
  const path = `Synaplan/Documents/${name}`
  cloudFiles.push(path)
  const { fileId, sent } = await sendNewFile(request, name)
  expect(sent.status, JSON.stringify(sent)).toBeLessThan(300)

  await expect
    .poll(() => downloadTextFile(request, webDavUrl, accessToken, path).catch(() => ''), {
      timeout: 30_000
    })
    .toBe(`copy of ${name}`)

  await expect
    .poll(
      async () => {
        const copies = await listFileCopies(request, SYNAPLAN_API_URL, synaplanToken, fileId)
        return copies.find((c) => String(c.id) === sent.deliveryId) ?? copies[0]
      },
      { timeout: 15_000 }
    )
    .toMatchObject({ state: 'delivered', remote_path: `/${path}` })

  // Signed in again, so afterEach can stop work while away without extra login.
  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
})

test('after stop, Synaplan cannot place a copy', async ({ request }) => {
  test.setTimeout(90_000)
  await stopWorkWhileAway(signedIn())

  const delegationResp = await request.get(`${OC_API_URL}/api/synaplan/delegation`, {
    headers: { Authorization: `Bearer ${accessToken}` }
  })
  expect(delegationResp.ok()).toBe(true)
  expect((await delegationResp.json()).connected).toBe(false)

  const name = `delivery-stopped-${Date.now()}.txt`
  const path = `Synaplan/Documents/${name}`
  cloudFiles.push(path)
  const { fileId, sent } = await sendNewFile(request, name)
  // Stopped work answers the notification with 409 not_connected; Synaplan
  // accepts the send (202) and leaves the copy queued rather than delivered.
  expect(sent.status, JSON.stringify(sent)).toBe(202)
  expect(sent.state).toBe('pending')

  const copies = await listFileCopies(request, SYNAPLAN_API_URL, synaplanToken, fileId)
  const copy = copies.find((c) => String(c.id) === sent.deliveryId) ?? copies[0]
  expect(copy?.state).toBe('queued')

  const deadline = Date.now() + 5_000
  const statuses: number[] = []
  await expect
    .poll(
      async () => {
        const probe = await request.get(`${webDavUrl}/${path}`, {
          headers: { Authorization: `Bearer ${accessToken}` }
        })
        const status = probe.status()
        statuses.push(status)
        if (status !== 404) return status
        return Date.now() >= deadline ? 404 : 0
      },
      { timeout: 10_000, intervals: [1_000] }
    )
    .toBe(404)
  expect(statuses.length).toBeGreaterThan(1)
  expect(statuses.every((status) => status === 404)).toBe(true)
})
