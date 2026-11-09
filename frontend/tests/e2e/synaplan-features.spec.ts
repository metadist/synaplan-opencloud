import { test, expect, Page } from '@playwright/test'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import { openSynaplanMenu } from '../../support/helpers/contextMenuHelper'
import {
  deleteFileQuiet,
  downloadTextFile,
  ensureFolder,
  getPersonalSpace
} from '../../support/helpers/filesHelper'
import { deleteSynaplanFileQuiet, uploadToSynaplan } from '../../support/helpers/synaplanApiHelper'

/**
 * The features a person uses while signed in, carried by the request's own
 * exchanged token: what Synaplan may read, a file from Synaplan into a
 * folder, an image made in Synaplan, and the administrator's choices and
 * registration on the Synaplan page.
 */

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const TESTS_DIR = 'synaplan-e2e'
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const SYNAPLAN_API_URL = process.env.SYNAPLAN_API_URL ?? 'http://localhost:8000'

test.describe.configure({ mode: 'serial' })

let page: Page

test.afterEach(async () => {
  if (!page) return
  await page.keyboard.press('Escape').catch(() => {})
  await page
    .locator('.oc-modal-title-actions-cancel')
    .click({ timeout: 2_000 })
    .catch(() => {})
  await logout(page).catch(() => {})
})

test('a person chooses what Synaplan may read, and Synaplan receives it', async ({ browser }) => {
  page = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
  await page.goto('/synaplan')
  const settings = page.locator('[data-testid="synaplan-settings"]')
  await expect(settings).toBeVisible({ timeout: 15_000 })

  await settings.getByLabel('All of my files').check()
  await page.locator('[data-testid="synaplan-save-read"]').click()
  await expect(page.locator('[data-testid="synaplan-read-saved"]')).toContainText(
    'Synaplan will follow this setting',
    { timeout: 15_000 }
  )

  await settings.getByLabel('Only what I watch').check()
  await page.locator('[data-testid="synaplan-save-read"]').click()
  await expect(page.locator('[data-testid="synaplan-read-saved"]')).toContainText(
    'Synaplan will follow this setting',
    { timeout: 15_000 }
  )
})

test('a file from Synaplan is copied into the chosen folder', async ({ browser, request }) => {
  test.setTimeout(60_000)
  const ocToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  const synaplanToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD, {
    clientId: 'synaplan-app',
    clientSecret: 'test-oidc-secret'
  })
  const webDavUrl = (await getPersonalSpace(request, OC_API_URL, ocToken)).webDavUrl
  await ensureFolder(request, webDavUrl, ocToken, TESTS_DIR)
  const folderName = `from-${Date.now()}`
  const folder = `${TESTS_DIR}/${folderName}`
  await ensureFolder(request, webDavUrl, ocToken, folder)
  const name = `from-synaplan-${Date.now()}.txt`
  const fileId = await uploadToSynaplan(
    request,
    SYNAPLAN_API_URL,
    synaplanToken,
    name,
    'made in Synaplan'
  )

  try {
    page = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
    await page.reload({ waitUntil: 'domcontentloaded' })
    await (await findRow(page, TESTS_DIR)).click()
    await openSynaplanMenu(await findRow(page, folderName))
    await page.locator('.oc-files-actions-from-synaplan-trigger').click()

    const dialog = page.locator('[data-testid="synaplan-from-dialog"]')
    await expect(dialog).toBeVisible({ timeout: 10_000 })
    await dialog.locator(`[data-testid="synaplan-from-file-${fileId}"]`).click()
    await dialog.locator('[data-testid="synaplan-from-submit"]').click()
    await expect(dialog.locator('[data-testid="synaplan-from-saved"]')).toBeVisible({
      timeout: 30_000
    })

    expect(await downloadTextFile(request, webDavUrl, ocToken, `${folder}/${name}`)).toBe(
      'made in Synaplan'
    )
  } finally {
    await deleteFileQuiet(request, webDavUrl, ocToken, folder)
    await deleteSynaplanFileQuiet(request, SYNAPLAN_API_URL, synaplanToken, fileId)
  }
})

test('an image made in Synaplan is saved into the files', async ({ browser }) => {
  test.setTimeout(90_000)
  page = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
  await page.goto('/synaplan')
  await page.locator('[data-testid="synaplan-image-prompt"]').fill('a red square')
  await page.locator('[data-testid="synaplan-image-make"]').click()
  await expect(page.locator('[data-testid="synaplan-image-note"]')).toContainText(
    'Saved into your files',
    { timeout: 60_000 }
  )
})

test('the administrator sees what Synaplan offers and saves the answer settings', async ({
  browser
}) => {
  page = (await loginAsUser(browser, 'admin', 'admin')).page
  await page.goto('/synaplan')
  await expect(page.locator('[data-testid="synaplan-instance-state"]')).toContainText(
    'Synaplan answers and offers: deliveries',
    { timeout: 15_000 }
  )
  await expect(page.locator('[data-testid="synaplan-admin-readonly"]')).toHaveCount(0)

  await page.locator('[data-testid="synaplan-admin-save"]').click()
  await expect(page.locator('[data-testid="synaplan-admin-saved"]')).toBeVisible({
    timeout: 15_000
  })
})

async function findRow(p: Page, name: string) {
  const row = p.locator(
    `#files-space-table [data-test-resource-name="${name}"], #tiles-view [data-test-resource-name="${name}"]`
  )
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  return row
}
