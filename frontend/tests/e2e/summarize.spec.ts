import { test, expect, Page } from '@playwright/test'
import { fileURLToPath } from 'url'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import {
  deleteFileQuiet,
  ensureFolder,
  getPersonalSpace,
  uploadFileFromDisk,
  uploadTextFile
} from '../../support/helpers/filesHelper'

const TESTS_DIR = 'synaplan-e2e'

const PDF_FIXTURE_PATH = fileURLToPath(
  new URL('../../support/filesForUpload/sample-lorem.pdf', import.meta.url)
)

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

test('summarize file context action is visible for the signed-in user', async ({ request }) => {
  const baseName = `ctxmenu-${Date.now()}.txt`
  const remotePath = `${TESTS_DIR}/${baseName}`
  await uploadTextFile(request, webDavUrl, accessToken, remotePath, 'hello')
  filesToCleanup.push(remotePath)

  await navigateToTestsFolder(userPage)

  const row = await findFileRow(userPage, baseName)
  await row.click({ button: 'right' })

  await expect(userPage.locator('.oc-files-actions-summarize-trigger')).toBeVisible({
    timeout: 10_000
  })

  await userPage.keyboard.press('Escape')
})

test('summarize a text file end-to-end', async ({ request }) => {
  test.setTimeout(60_000)

  const baseName = `text-${Date.now()}.txt`
  const remotePath = `${TESTS_DIR}/${baseName}`
  const fileText =
    'The quick brown fox jumps over the lazy dog. ' +
    'This sentence is famous because it contains every letter of the alphabet.'
  await uploadTextFile(request, webDavUrl, accessToken, remotePath, fileText)
  filesToCleanup.push(remotePath)

  await navigateToTestsFolder(userPage)
  await runSummarizeFlow(userPage, baseName, 'Bullet points', 'Short')
})

test('summarize a PDF file end-to-end', async ({ request }) => {
  // Binary documents hit the upload-for-extraction path (the backend
  // pushes the bytes to Synaplan with process_level=extract and then
  // calls /summary/generate by fileId). Depends on Tika being up.
  test.setTimeout(60_000)

  const baseName = `pdf-${Date.now()}.pdf`
  const remotePath = `${TESTS_DIR}/${baseName}`
  await uploadFileFromDisk(
    request,
    webDavUrl,
    accessToken,
    remotePath,
    PDF_FIXTURE_PATH,
    'application/pdf'
  )
  filesToCleanup.push(remotePath)

  await navigateToTestsFolder(userPage)
  await runSummarizeFlow(userPage, baseName, 'Abstractive', 'Medium')
})

async function runSummarizeFlow(
  page: Page,
  fileName: string,
  summaryTypeLabel: string,
  lengthLabel: string
) {
  const row = await findFileRow(page, fileName)
  await row.click({ button: 'right' })

  await page.locator('.oc-files-actions-summarize-trigger').click()

  const dialog = page.locator('[data-testid="synaplan-summarize-dialog"]')
  await expect(dialog).toBeVisible({ timeout: 10_000 })

  await page.locator('[data-testid="synaplan-summarize-type"] .vs__dropdown-toggle').first().click()
  await page.getByRole('option', { name: summaryTypeLabel }).click()

  await page
    .locator('[data-testid="synaplan-summarize-length"] .vs__dropdown-toggle')
    .first()
    .click()
  await page.getByRole('option', { name: lengthLabel }).click()

  await page.locator('[data-testid="synaplan-summarize-submit"]').click()

  const result = page.locator('[data-testid="synaplan-summarize-result"]')
  await expect(result).toBeVisible({ timeout: 30_000 })
  const text = await result.textContent()
  expect(text?.trim().length ?? 0).toBeGreaterThan(0)

  await page.locator('.oc-modal-title-actions-cancel').click()
  await expect(dialog).toBeHidden()
}

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
