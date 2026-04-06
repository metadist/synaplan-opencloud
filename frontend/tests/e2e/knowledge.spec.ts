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
import { wipeKnowledgeGroup } from '../../support/helpers/synaplanApiHelper'

const TESTS_DIR = 'synaplan-e2e'

const PDF_FIXTURE_PATH = fileURLToPath(
  new URL('../../support/filesForUpload/sample-lorem.pdf', import.meta.url)
)

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'

// Fixed group name across all runs — beforeEach wipes it via the
// Synaplan API so the test always exercises the "create new" flow
// from a clean slate, and CI doesn't accumulate group entries.
const KNOWLEDGE_GROUP = 'OC_E2E_TEST'

const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const SYNAPLAN_API_URL = process.env.SYNAPLAN_API_URL ?? 'http://host.docker.internal:8001'

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

  // Wipe any leftover files in the test group via Synaplan's own API
  // so the dialog always opens against an empty group.
  const synaplanToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD, {
    clientId: 'synaplan-app',
    clientSecret: 'test-oidc-secret'
  })
  await wipeKnowledgeGroup(request, SYNAPLAN_API_URL, synaplanToken, KNOWLEDGE_GROUP)

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

test('add-to-knowledge context action is visible for the signed-in user', async ({ request }) => {
  const baseName = `ctxmenu-${Date.now()}.txt`
  const remotePath = `${TESTS_DIR}/${baseName}`
  await uploadTextFile(request, webDavUrl, accessToken, remotePath, 'hello')
  filesToCleanup.push(remotePath)

  await navigateToTestsFolder(userPage)

  const row = await findFileRow(userPage, baseName)
  await row.click({ button: 'right' })

  await expect(userPage.locator('.oc-files-actions-add-to-knowledge-trigger')).toBeVisible({
    timeout: 10_000
  })

  await userPage.keyboard.press('Escape')
})

test('add a text file to a knowledge group end-to-end', async ({ request }) => {
  test.setTimeout(60_000)

  const baseName = `text-${Date.now()}.txt`
  const remotePath = `${TESTS_DIR}/${baseName}`
  await uploadTextFile(
    request,
    webDavUrl,
    accessToken,
    remotePath,
    'Knowledge base smoke test: one sentence is enough to vectorize.'
  )
  filesToCleanup.push(remotePath)

  await navigateToTestsFolder(userPage)
  await runKnowledgeFlow(userPage, baseName, KNOWLEDGE_GROUP)
})

test('add a PDF file to a knowledge group end-to-end', async ({ request }) => {
  // Binary docs exercise the extract + vectorize path (Tika pipeline).
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
  await runKnowledgeFlow(userPage, baseName, KNOWLEDGE_GROUP)
})

async function runKnowledgeFlow(page: Page, fileName: string, groupKey: string) {
  const row = await findFileRow(page, fileName)
  await row.click({ button: 'right' })

  await page.locator('.oc-files-actions-add-to-knowledge-trigger').click()

  const dialog = page.locator('[data-testid="synaplan-knowledge-dialog"]')
  await expect(dialog).toBeVisible({ timeout: 10_000 })

  // The group picker is a taggable oc-select wrapping vue-select.
  // fill() doesn't fire the input events vue-select needs to build
  // its create-option ghost entry, so click the toggle to open +
  // focus the search input, type with real keystrokes, then click
  // the highlighted create option in the dropdown. createOption
  // normalises to upper case so we assert on that.
  const picker = page.locator('[data-testid="synaplan-knowledge-group"]')
  await picker.locator('.vs__dropdown-toggle').click()
  await page.keyboard.type(groupKey)
  await picker.locator('.vs__dropdown-option').first().click()

  // Submit button enables once vue-select has emitted option:created.
  const submit = page.locator('[data-testid="synaplan-knowledge-submit"]')
  await expect(submit).toBeEnabled({ timeout: 5_000 })
  await submit.click()

  const success = page.locator('[data-testid="synaplan-knowledge-success"]')
  await expect(success).toBeVisible({ timeout: 30_000 })
  await expect(success).toContainText(groupKey.toUpperCase())

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
