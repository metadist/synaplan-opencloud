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
import { defaultChatModelLabel } from '../../support/helpers/synaplanApiHelper'

/**
 * Ask Synaplan about a file: the context action is visible, the dialog
 * answers a question about a text file and a PDF (Tika extraction) with
 * a model the person picked, and a file without text says so in one
 * sentence. The journeys assert the terminal state, never the wording.
 */

const TESTS_DIR = 'synaplan-e2e'
const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const SYNAPLAN_API_URL = process.env.SYNAPLAN_API_URL ?? 'http://host.docker.internal:8000'
const PDF_FIXTURE_PATH = fileURLToPath(
  new URL('../../support/filesForUpload/sample-lorem.pdf', import.meta.url)
)

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

test('ask-about-file context action is visible for the signed-in user', async ({ request }) => {
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

test('ask a question about a text file with a picked model', async ({ request }) => {
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

  const synaplanToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD, {
    clientId: 'synaplan-app',
    clientSecret: 'test-oidc-secret'
  })
  const modelLabel = await defaultChatModelLabel(request, SYNAPLAN_API_URL, synaplanToken)

  await navigateToTestsFolder(userPage)
  await openChatDialog(userPage, baseName)
  await pickModel(userPage, modelLabel)
  await askAndExpectAnswer(userPage, 'Who sent this invoice?')
})

test('ask a question about a PDF file', async ({ request }) => {
  // A PDF goes through Synaplan's extraction (Tika) and the extracted
  // text is read back before the question is asked.
  test.setTimeout(90_000)

  const baseName = `chat-pdf-${Date.now()}.pdf`
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
  await openChatDialog(userPage, baseName)
  await askAndExpectAnswer(userPage, 'What is this document about?')
})

test('asking about a file without text says so in one sentence', async ({ request }) => {
  const baseName = `chat-blank-${Date.now()}.txt`
  const remotePath = `${TESTS_DIR}/${baseName}`
  await uploadTextFile(request, webDavUrl, accessToken, remotePath, '   \n')
  filesToCleanup.push(remotePath)

  await navigateToTestsFolder(userPage)
  await openChatDialog(userPage, baseName)
  await userPage.locator('[data-testid="synaplan-chat-question"]').fill('What is in it?')
  await userPage.locator('[data-testid="synaplan-chat-submit"]').click()

  await expect(userPage.locator('[data-testid="synaplan-chat-error"]')).toHaveText(
    'This file is empty.',
    { timeout: 15_000 }
  )
  await expect(userPage.locator('[data-testid="synaplan-chat-result"]')).toBeHidden()
  await expect(userPage.locator('[data-testid="synaplan-chat-loading"]')).toBeHidden()
  await userPage.locator('.oc-modal-title-actions-cancel').click()
})

async function openChatDialog(page: Page, fileName: string) {
  const models = page.waitForResponse(
    (r) => r.url().endsWith('/api/synaplan/models') && r.request().method() === 'GET'
  )
  const row = await findFileRow(page, fileName)
  await openSynaplanMenu(row)
  await page.locator('.oc-files-actions-chat-about-file-trigger').click()
  await expect(page.locator('[data-testid="synaplan-chat-dialog"]')).toBeVisible({
    timeout: 10_000
  })
  expect((await models).ok()).toBe(true)
}

// Picks a model by the label the dialog shows; "Default model" is
// always the first option.
async function pickModel(page: Page, label: string) {
  await page.locator('[data-testid="synaplan-chat-model"] .vs__dropdown-toggle').first().click()
  await expect(page.getByRole('option').first()).toHaveText('Default model')
  await page.getByRole('option', { name: label, exact: true }).click()
  await expect(page.locator('[data-testid="synaplan-chat-model"]')).toContainText(label)
}

async function askAndExpectAnswer(page: Page, question: string) {
  const submit = page.locator('[data-testid="synaplan-chat-submit"]')
  await expect(submit).toBeDisabled()
  await page.locator('[data-testid="synaplan-chat-question"]').fill(question)
  await expect(submit).toBeEnabled()
  await submit.click()

  const result = page.locator('[data-testid="synaplan-chat-result"]')
  await expect(result).toBeVisible({ timeout: 60_000 })
  await expect(result).not.toBeEmpty()
  await expect(page.locator('[data-testid="synaplan-chat-loading"]')).toBeHidden()
  await expect(page.locator('[data-testid="synaplan-chat-error"]')).toBeHidden()

  await page.locator('.oc-modal-title-actions-cancel').click()
  await expect(page.locator('[data-testid="synaplan-chat-dialog"]')).toBeHidden()
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
