import { APIRequestContext, Page, expect, test } from '@playwright/test'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import {
  deleteFileQuiet,
  ensureFolder,
  getPersonalSpace,
  getResourceId
} from '../../support/helpers/filesHelper'

/**
 * A person's own Synaplan settings: on the Synaplan page they choose what
 * Synaplan may read and remove a folder they allowed. The
 * Go service reports each change to Synaplan as the person and keeps it
 * in OpenCloud's NATS only once Synaplan took it, so a saved setting that
 * survives a reload has crossed OpenCloud, the token exchange and
 * Synaplan.
 */

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const TESTS_DIR = 'synaplan-e2e'
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'

test.describe.configure({ mode: 'serial' })

let userPage: Page
let webDavUrl: string
let accessToken: string
const foldersToCleanup: string[] = []

test.beforeEach(async ({ browser, request }) => {
  accessToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  webDavUrl = (await getPersonalSpace(request, OC_API_URL, accessToken)).webDavUrl
  foldersToCleanup.length = 0
  await ensureFolder(request, webDavUrl, accessToken, TESTS_DIR)
  await resetSetting(request)
  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
})

test.afterEach(async ({ request }) => {
  await resetSetting(request).catch(() => {})
  for (const folder of foldersToCleanup) {
    await deleteFileQuiet(request, webDavUrl, accessToken, folder)
  }
  if (userPage) {
    await userPage.keyboard.press('Escape').catch(() => {})
    await logout(userPage).catch(() => {})
  }
})

async function resetSetting(request: APIRequestContext) {
  const resp = await request.put(`${OC_API_URL}/api/synaplan/preferences`, {
    headers: { Authorization: `Bearer ${accessToken}` },
    data: { mode: 'watched', folders: [] }
  })
  expect(resp.ok(), await resp.text()).toBe(true)
}

async function openSettings(page: Page) {
  await page.goto('/synaplan')
  await expect(page.locator('[data-testid="synaplan-settings-save"]')).toBeVisible({
    timeout: 15_000
  })
}

async function saveSettings(page: Page) {
  await page.locator('[data-testid="synaplan-settings-save"]').click()
  await expect(page.locator('[data-testid="synaplan-settings-saved"]')).toBeVisible({
    timeout: 15_000
  })
  await expect(page.locator('[data-testid="synaplan-settings-error"]')).toHaveCount(0)
}

async function seedFolder(request: APIRequestContext, prefix: string) {
  const name = `${prefix}-${Date.now()}`
  const remotePath = `${TESTS_DIR}/${name}`
  await ensureFolder(request, webDavUrl, accessToken, remotePath)
  foldersToCleanup.push(remotePath)
  return remotePath
}

async function allowFolder(request: APIRequestContext, id: string) {
  const resp = await request.put(`${OC_API_URL}/api/synaplan/preferences`, {
    headers: { Authorization: `Bearer ${accessToken}` },
    data: { mode: 'allowed', folders: [{ id, path: 'Everything' }] }
  })
  expect(resp.ok(), await resp.text()).toBe(true)
  return (await resp.json()).readScope.folders as { id: string; path: string }[]
}

test('a person chooses what Synaplan may read, and the choice is kept', async () => {
  await openSettings(userPage)
  await expect(userPage.locator('[data-testid="synaplan-settings-login"]')).toHaveText(
    'Your OpenCloud login is your Synaplan login.'
  )
  await expect(userPage.locator('[data-testid="synaplan-settings-read-watched"]')).toBeChecked()

  await userPage.locator('[data-testid="synaplan-settings-read-all"]').check()
  await saveSettings(userPage)

  await openSettings(userPage)
  await expect(userPage.locator('[data-testid="synaplan-settings-read-all"]')).toBeChecked()

  await userPage.locator('[data-testid="synaplan-settings-read-watched"]').check()
  await saveSettings(userPage)
})

test('an allowed folder carries its OpenCloud path and is removed on the Synaplan page', async ({
  request
}) => {
  const remotePath = await seedFolder(request, 'read')
  const id = await getResourceId(request, webDavUrl, accessToken, remotePath)
  expect(await allowFolder(request, id)).toEqual([{ id, path: remotePath }])

  await openSettings(userPage)
  await expect(userPage.locator('[data-testid="synaplan-settings-read-allowed"]')).toBeChecked()
  const folder = userPage.locator('[data-testid="synaplan-settings-folder"]')
  await expect(folder).toHaveCount(1)
  await expect(folder).toContainText(remotePath)

  await folder.locator('[data-testid="synaplan-settings-folder-remove"]').click()
  await userPage.locator('[data-testid="synaplan-settings-read-watched"]').check()
  await saveSettings(userPage)

  await openSettings(userPage)
  await expect(userPage.locator('[data-testid="synaplan-settings-read-watched"]')).toBeChecked()
  await userPage.locator('[data-testid="synaplan-settings-read-allowed"]').check()
  await expect(folder).toHaveCount(0)
})

test('a folder deleted after it was allowed leaves the setting on the next save', async ({
  request
}) => {
  const remotePath = await seedFolder(request, 'gone')
  const id = await getResourceId(request, webDavUrl, accessToken, remotePath)
  await allowFolder(request, id)
  await deleteFileQuiet(request, webDavUrl, accessToken, remotePath)

  await openSettings(userPage)
  const folder = userPage.locator('[data-testid="synaplan-settings-folder"]')
  await expect(folder).toHaveCount(1)
  await saveSettings(userPage)
  await expect(folder).toHaveCount(0)
})
