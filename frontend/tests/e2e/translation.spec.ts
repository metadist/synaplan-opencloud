import { test, expect, Page } from '@playwright/test'
import { fileURLToPath } from 'url'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import { openSynaplanMenu } from '../../support/helpers/contextMenuHelper'
import {
  deleteFileQuiet,
  ensureFolder,
  getPersonalSpace,
  uploadFileFromDisk,
  uploadTextFile
} from '../../support/helpers/filesHelper'
import { findFileRow, navigateToTestsFolder } from '../../support/helpers/filesViewHelper'

// All fixture files land in this subdir of the user's personal space
// so they don't clutter the test user's root listing. The folder is
// MKCOL'd idempotently in beforeEach and kept between runs.
const TESTS_DIR = 'synaplan-e2e'

const PDF_FIXTURE_PATH = fileURLToPath(
  new URL('../../support/filesForUpload/sample-lorem.pdf', import.meta.url)
)

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'

// When running against the opencloud-eu/web dev server on :9201 the
// browser loads the SPA from there, but API requests (graph, WebDAV)
// still have to hit the real OC backend on :9200. `baseURL` can't be
// reused for API setup — this is the explicit fixture target.
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'

let userPage: Page
let webDavUrl: string
let accessToken: string
// Files uploaded during a test — afterEach drops them so the user's
// personal space stays clean across runs.
const filesToCleanup: string[] = []

test.beforeEach(async ({ browser, request }) => {
  accessToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  const space = await getPersonalSpace(request, OC_API_URL, accessToken)
  webDavUrl = space.webDavUrl
  filesToCleanup.length = 0

  // Keep test fixtures in a dedicated subdir of the personal space.
  await ensureFolder(request, webDavUrl, accessToken, TESTS_DIR)

  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
})

test.afterEach(async ({ request }) => {
  for (const fn of filesToCleanup) {
    await deleteFileQuiet(request, webDavUrl, accessToken, fn)
  }
  if (userPage) {
    // Dismiss any lingering modal so its backdrop doesn't intercept
    // clicks on the account menu during logout. Harmless if no modal
    // is open.
    await userPage.keyboard.press('Escape').catch(() => {})
    await logout(userPage).catch(() => {})
  }
})

test('translate file context action is visible for the signed-in user', async ({ request }) => {
  const baseName = `ctxmenu-${Date.now()}.txt`
  const remotePath = `${TESTS_DIR}/${baseName}`
  await uploadTextFile(request, webDavUrl, accessToken, remotePath, 'hello')
  filesToCleanup.push(remotePath)

  await navigateToTestsFolder(userPage, TESTS_DIR)

  const row = await findFileRow(userPage, baseName)
  await openSynaplanMenu(row)

  await expect(userPage.locator('.oc-files-actions-translate-trigger')).toBeVisible({
    timeout: 10_000
  })

  // Dismiss the context menu so afterEach's logout isn't blocked.
  await userPage.keyboard.press('Escape')
})

test('translate a text file end-to-end', async ({ request }) => {
  test.setTimeout(60_000)

  const baseName = `text-${Date.now()}.txt`
  const remotePath = `${TESTS_DIR}/${baseName}`
  const fileText =
    'The quick brown fox jumps over the lazy dog. ' +
    'This sentence is famous because it contains every letter of the alphabet.'
  await uploadTextFile(request, webDavUrl, accessToken, remotePath, fileText)
  filesToCleanup.push(remotePath)

  await navigateToTestsFolder(userPage, TESTS_DIR)
  await runTranslationFlow(userPage, baseName, 'Deutsch')
})

test('translate a PDF file end-to-end', async ({ request }) => {
  // Binary documents exercise a different code path: the backend
  // uploads the bytes to Synaplan with process_level=extract, then
  // calls summary/generate by fileId instead of inlining the text.
  // This path depends on Synaplan's Tika extraction service.
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

  await navigateToTestsFolder(userPage, TESTS_DIR)
  await runTranslationFlow(userPage, baseName, 'Deutsch')
})

/**
 * Shared happy-path flow: find the file in the list, fire the context
 * action, pick a target language, submit, and assert the result box
 * shows up with non-empty text. Closes the modal at the end so
 * afterEach isn't blocked by the OcModal backdrop.
 */
async function runTranslationFlow(page: Page, fileName: string, languageLabel: string) {
  const row = await findFileRow(page, fileName)
  await openSynaplanMenu(row)

  // Target the action by its stable OC trigger class instead of
  // by-text — by-text occasionally flakes when the context menu
  // popup is positioned over the left nav sidebar.
  await page.locator('.oc-files-actions-translate-trigger').click()

  const dialog = page.locator('[data-testid="synaplan-translation-dialog"]')
  await expect(dialog).toBeVisible({ timeout: 10_000 })

  // OcSelect wraps vue-select — click the inner dropdown toggle and
  // pick the option by visible label.
  await page
    .locator('[data-testid="synaplan-translation-language"] .vs__dropdown-toggle')
    .first()
    .click()
  await page.getByRole('option', { name: languageLabel }).click()

  await page.locator('[data-testid="synaplan-translation-submit"]').click()

  const result = page.locator('[data-testid="synaplan-translation-result"]')
  await expect(result).toBeVisible({ timeout: 30_000 })
  const text = await result.textContent()
  expect(text?.trim().length ?? 0).toBeGreaterThan(0)

  // The modal has no explicit Close button — dismissal goes through
  // OcModal's built-in title-bar X.
  await page.locator('.oc-modal-title-actions-cancel').click()
  await expect(dialog).toBeHidden()
}
