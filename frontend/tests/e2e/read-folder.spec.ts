import { APIRequestContext, Page, expect, test } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import { openSynaplanMenu } from '../../support/helpers/contextMenuHelper'
import { allowWorkWhileAway, stopWorkWhileAway } from '../../support/helpers/delegationHelper'
import {
  deleteFileQuiet,
  ensureFolder,
  getPersonalSpace,
  getResourceId,
  uploadTextFile
} from '../../support/helpers/filesHelper'

/**
 * Folder reads ("pickups"): a task in Synaplan asks this OpenCloud for the
 * files in a folder. The person allows a folder in the read-folder dialog;
 * the Go service reports the setting to Synaplan, which keeps it. When a
 * read comes, Synaplan sends a signed notification, and the service claims
 * the read with the person's own Synaplan token, finds the folder by its
 * path as the person (CS3 with a reva token from machine auth, nobody is
 * signed in), checks the setting, lists the folder and uploads the files
 * Synaplan asks for.
 *
 * Synaplan opens a read only from a task's folder step, which needs a
 * planning model; its dev and test command `app:cloud-pickups:open-test`
 * opens the same read without one and prints the outcome.
 */

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const TESTS_DIR = 'synaplan-e2e'
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const SYNAPLAN_API_URL = process.env.SYNAPLAN_API_URL ?? 'http://host.docker.internal:8000'
const SYNAPLAN_CONTAINER = process.env.SYNAPLAN_APP_CONTAINER ?? 'synaplan-app-test'

// Serial: the tests share the person's one read setting in Synaplan.
test.describe.configure({ mode: 'serial' })

let userPage: Page
let webDavUrl: string
let accessToken: string
let folder = ''
let folderName = ''

interface ReadOutcome {
  state: string
  sent?: number
  names?: string[]
  error?: string
}

function openRead(folderPath: string): ReadOutcome {
  let out: string
  try {
    out = execFileSync('docker', [
      'exec',
      SYNAPLAN_CONTAINER,
      'php',
      'bin/console',
      'app:cloud-pickups:open-test',
      `--email=${TEST_USER}`,
      '--client=opencloud',
      `--folder=${folderPath}`,
      '--mode=fetch'
    ]).toString()
  } catch (e) {
    // A refused read exits non-zero and still prints its JSON line.
    out = String((e as { stdout?: Buffer }).stdout ?? '')
  }
  return JSON.parse(out.trim().split('\n').pop() || '{}')
}

async function setRead(request: APIRequestContext, allow: boolean) {
  const folderId = await getResourceId(request, webDavUrl, accessToken, folder)
  const resp = await request.put(`${OC_API_URL}/api/synaplan/read-scope`, {
    headers: { Authorization: `Bearer ${accessToken}` },
    data: { folderId, allow }
  })
  expect(resp.ok(), await resp.text()).toBe(true)
  return resp.json()
}

// The folders Synaplan keeps as readable for the person's link to this
// OpenCloud, by path.
async function synaplanReadFolders(request: APIRequestContext): Promise<string[]> {
  const token = await getAccessToken(request, TEST_USER, TEST_PASSWORD, {
    clientId: 'synaplan-app',
    clientSecret: 'test-oidc-secret'
  })
  const resp = await request.get(`${SYNAPLAN_API_URL}/api/v1/me/platform-links`, {
    headers: { Authorization: `Bearer ${token}` }
  })
  expect(resp.ok(), await resp.text()).toBe(true)
  const links = (await resp.json()).links as {
    client: string
    status: string
    read_scope?: { folders: string[] }
  }[]
  const link = links.find((l) => l.client === 'opencloud' && l.status === 'active')
  return link?.read_scope?.folders ?? []
}

test.beforeEach(async ({ browser, request }) => {
  accessToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  webDavUrl = (await getPersonalSpace(request, OC_API_URL, accessToken)).webDavUrl
  await ensureFolder(request, webDavUrl, accessToken, TESTS_DIR)
  folderName = `read-${Date.now()}`
  folder = `${TESTS_DIR}/${folderName}`
  await ensureFolder(request, webDavUrl, accessToken, folder)
  await uploadTextFile(request, webDavUrl, accessToken, `${folder}/invoice-1.txt`, 'invoice one')
  await uploadTextFile(request, webDavUrl, accessToken, `${folder}/invoice-2.txt`, 'invoice two')
  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
})

test.afterEach(async ({ request }) => {
  // A journey that failed half way must not leave the folder readable.
  await setRead(request, false).catch(() => {})
  await deleteFileQuiet(request, webDavUrl, accessToken, folder)
  if (userPage) {
    await userPage.keyboard.press('Escape').catch(() => {})
    await stopWorkWhileAway(userPage).catch(() => {})
    await logout(userPage).catch(() => {})
  }
})

test('allow a folder to be read, Synaplan keeps it, stop', async ({ request }) => {
  test.setTimeout(60_000)

  await navigateToTestsFolder(userPage)
  const row = await findRow(userPage, folderName)
  await openSynaplanMenu(row)
  await userPage.locator('.oc-files-actions-read-folder-trigger').click()

  const dialog = userPage.locator('[data-testid="synaplan-read-dialog"]')
  await expect(dialog).toBeVisible({ timeout: 10_000 })
  await expect(userPage.locator('[data-testid="synaplan-read-not-allowed"]')).toBeVisible({
    timeout: 15_000
  })

  await userPage.locator('[data-testid="synaplan-read-allow"]').click()
  await expect(userPage.locator('[data-testid="synaplan-read-allowed"]')).toBeVisible({
    timeout: 15_000
  })
  await expect(userPage.locator('[data-testid="synaplan-read-error"]')).toBeHidden()
  // Synaplan keeps the folder by the path OpenCloud knows for it.
  expect(await synaplanReadFolders(request)).toContain(folder)

  await userPage.locator('[data-testid="synaplan-read-stop"]').click()
  await expect(userPage.locator('[data-testid="synaplan-read-not-allowed"]')).toBeVisible({
    timeout: 15_000
  })
  expect(await synaplanReadFolders(request)).not.toContain(folder)

  await userPage.locator('.oc-modal-title-actions-cancel').click()
  await expect(dialog).toBeHidden()
})

test('the read-folder action is only offered on a folder', async ({ request }) => {
  await uploadTextFile(request, webDavUrl, accessToken, `${TESTS_DIR}/${folderName}.txt`, 'x')
  try {
    await navigateToTestsFolder(userPage)
    await openSynaplanMenu(await findRow(userPage, `${folderName}.txt`))
    await expect(userPage.locator('.oc-files-actions-watch-trigger')).toBeVisible({
      timeout: 10_000
    })
    await expect(userPage.locator('.oc-files-actions-read-folder-trigger')).toHaveCount(0)
  } finally {
    await deleteFileQuiet(request, webDavUrl, accessToken, `${TESTS_DIR}/${folderName}.txt`)
  }
})

test('Synaplan reads nothing from a folder the person did not allow', async () => {
  const outcome = openRead(folder)
  expect(outcome.state).toBe('refused')
  expect(outcome.sent ?? 0).toBe(0)
})

test('Synaplan reads an allowed folder while the person is away', async ({ browser, request }) => {
  test.setTimeout(120_000)
  await allowWorkWhileAway(userPage)
  const scope = await setRead(request, true)
  test.skip(
    !scope.readsWhileAway,
    'The Go service has no OC_MACHINE_AUTH_API_KEY, so it cannot read files while the person is away.'
  )
  // Nobody is signed in while Synaplan reads.
  await logout(userPage)

  const outcome = openRead(folder)
  expect(outcome.state, JSON.stringify(outcome)).toBe('done')
  expect(outcome.sent).toBe(2)
  expect((outcome.names ?? []).sort()).toEqual(['invoice-1.txt', 'invoice-2.txt'])

  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
})

async function findRow(page: Page, name: string) {
  const row = page.locator(
    `#files-space-table [data-test-resource-name="${name}"], #tiles-view [data-test-resource-name="${name}"]`
  )
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  return row
}

async function navigateToTestsFolder(page: Page) {
  await page.reload({ waitUntil: 'domcontentloaded' })
  const folderRow = await findRow(page, TESTS_DIR)
  await folderRow.click()
}
