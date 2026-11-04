import { test, expect, Page } from '@playwright/test'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import { openSynaplanMenu } from '../../support/helpers/contextMenuHelper'
import {
  deleteFileQuiet,
  ensureFolder,
  getPersonalSpace,
  uploadTextFile
} from '../../support/helpers/filesHelper'

/**
 * Chat about a file: the context action is visible, the dialog answers
 * a question with a non-empty result, and the dialog closes. The
 * journey asserts the terminal state, never the model's wording.
 */

const TESTS_DIR = 'synaplan-e2e'
const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'

let userPage: Page
let webDavUrl: string
let accessToken: string
const filesToCleanup: string[] = []

test.beforeEach(async ({ browser, request }) => {
  accessToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  const space = await getPersonalSpace(request, OC_API_URL, accessToken)
  webDavUrl = space.webDavUrl
  filesToCleanup.length = 0
  await ensureFolder(request, webDavUrl, accessToken, TESTS_DIR)
  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
})

test.afterEach(async ({ request }) => {
  for (const fn of filesToCleanup) {
    await deleteFileQuiet(request, webDavUrl, accessToken, fn)
  }
  if (userPage) {
    await userPage.keyboard.press('Escape').catch(() => {})
    await logout(userPage).catch(() => {})
  }
})

test('chat-about-file context action is visible for the signed-in user', async ({ request }) => {
  const baseName = `chat-menu-${Date.now()}.txt`
  const remotePath = `${TESTS_DIR}/${baseName}`
  await uploadTextFile(request, webDavUrl, accessToken, remotePath, 'hello')
  filesToCleanup.push(remotePath)

  await navigateToTestsFolder(userPage)
  const row = await findFileRow(userPage, baseName)
  await openSynaplanMenu(row)

  await expect(userPage.locator('.oc-files-actions-chat-about-file-trigger')).toBeVisible({
    timeout: 10_000
  })
  await userPage.keyboard.press('Escape')
})

test('ask a question about a text file and get an answer', async ({ request }) => {
  test.setTimeout(90_000)

  const baseName = `chat-${Date.now()}.txt`
  const remotePath = `${TESTS_DIR}/${baseName}`
  await uploadTextFile(
    request,
    webDavUrl,
    accessToken,
    remotePath,
    'Invoice 2026-10-29 from Nordwind GmbH. Total: 1310 EUR for 40 office chairs.'
  )
  filesToCleanup.push(remotePath)

  await navigateToTestsFolder(userPage)
  const row = await findFileRow(userPage, baseName)
  await openSynaplanMenu(row)
  await userPage.locator('.oc-files-actions-chat-about-file-trigger').click()

  const dialog = userPage.locator('[data-testid="synaplan-chat-dialog"]')
  await expect(dialog).toBeVisible({ timeout: 10_000 })

  const submit = userPage.locator('[data-testid="synaplan-chat-submit"]')
  await expect(submit).toBeDisabled()
  await userPage.locator('[data-testid="synaplan-chat-question"]').fill('Who sent this invoice?')
  await expect(submit).toBeEnabled()
  await submit.click()

  // Terminal state: an answer or one readable sentence, never a stuck spinner.
  const result = userPage.locator('[data-testid="synaplan-chat-result"]')
  await expect(result).toBeVisible({ timeout: 60_000 })
  await expect(result).not.toBeEmpty()
  await expect(userPage.locator('[data-testid="synaplan-chat-loading"]')).toBeHidden()
  await expect(userPage.locator('[data-testid="synaplan-chat-error"]')).toBeHidden()

  await userPage.locator('.oc-modal-title-actions-cancel').click()
  await expect(dialog).toBeHidden()
})

async function findFileRow(page: Page, name: string) {
  const row = page.locator(
    `#files-space-table [data-test-resource-name="${name}"], #tiles-view [data-test-resource-name="${name}"]`
  )
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  return row
}

async function navigateToTestsFolder(page: Page) {
  await page.reload({ waitUntil: 'domcontentloaded' })
  const folderRow = await findFileRow(page, TESTS_DIR)
  await folderRow.click()
}
