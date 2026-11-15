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

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const TESTS_DIR = 'synaplan-e2e'

const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const SYNAPLAN_API_URL = process.env.SYNAPLAN_API_URL ?? 'http://localhost:8000'

let page: Page | undefined

test.afterEach(async () => {
  if (!page) return
  await page.keyboard.press('Escape').catch(() => {})
  await logout(page).catch(() => {})
  page = undefined
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

async function findRow(page: Page, name: string) {
  const row = page.locator(
    `#files-space-table [data-test-resource-name="${name}"], #tiles-view [data-test-resource-name="${name}"]`
  )
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  return row
}
