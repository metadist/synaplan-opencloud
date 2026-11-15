import { test, expect, Page } from '@playwright/test'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import { openSynaplanMenu } from '../../support/helpers/contextMenuHelper'
import {
  deleteFileQuiet,
  ensureFolder,
  getPersonalSpace,
  getResourceId,
  uploadTextFile
} from '../../support/helpers/filesHelper'
import { findFileRow, navigateToFolder } from '../../support/helpers/filesPage'
import { knowledgeFileNames, wipeKnowledgeGroup } from '../../support/helpers/synaplanApiHelper'

const TESTS_DIR = 'synaplan-e2e'
const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'

// Fixed group, wiped before each test, so the knowledge folder only ever
// holds what this run sent.
const WATCH_GROUP = 'OC_E2E_WATCH'

// Serial: the tests share WATCH_GROUP and Synaplan's per-person watch cap.
test.describe.configure({ mode: 'serial' })

const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const SYNAPLAN_API_URL = process.env.SYNAPLAN_API_URL ?? 'http://host.docker.internal:8000'

let userPage: Page
let webDavUrl: string
let accessToken: string
let synaplanToken: string
let watchedFolder = ''
let watchedFile = ''

test.beforeEach(async ({ browser, request }) => {
  accessToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  webDavUrl = (await getPersonalSpace(request, OC_API_URL, accessToken)).webDavUrl
  await ensureFolder(request, webDavUrl, accessToken, TESTS_DIR)
  watchedFolder = ''
  watchedFile = ''

  synaplanToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD, {
    clientId: 'synaplan-app',
    clientSecret: 'test-oidc-secret'
  })
  await wipeKnowledgeGroup(request, SYNAPLAN_API_URL, synaplanToken, WATCH_GROUP)

  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
})

test.afterEach(async ({ request }) => {
  // A journey that failed half way must not leave a watch behind: it
  // counts against the person's cap in Synaplan. The file is deleted
  // before its folder.
  for (const path of [watchedFile, watchedFolder]) {
    if (!path) continue
    const targetId = await getResourceId(request, webDavUrl, accessToken, path).catch(() => '')
    if (targetId) {
      await request
        .delete(`${OC_API_URL}/api/synaplan/watches`, {
          params: { targetId },
          headers: { Authorization: `Bearer ${accessToken}` }
        })
        .catch(() => {})
    }
    await deleteFileQuiet(request, webDavUrl, accessToken, path)
  }
  await wipeKnowledgeGroup(request, SYNAPLAN_API_URL, synaplanToken, WATCH_GROUP).catch(() => {})
  if (userPage) {
    await userPage.keyboard.press('Escape').catch(() => {})
    await logout(userPage).catch(() => {})
  }
})

test('watch context action is visible on a folder', async ({ request }) => {
  watchedFolder = `${TESTS_DIR}/watch-menu-${Date.now()}`
  await ensureFolder(request, webDavUrl, accessToken, watchedFolder)

  await navigateToFolder(userPage, TESTS_DIR)
  const row = await findFileRow(userPage, watchedFolder.split('/')[1])
  await openSynaplanMenu(row)

  await expect(userPage.locator('.oc-files-actions-watch-trigger')).toBeVisible({
    timeout: 10_000
  })
  await userPage.keyboard.press('Escape')
})

test('watch a folder, check now, the file is in Synaplan, stop', async ({ request }) => {
  test.setTimeout(90_000)

  const folderName = `watch-${Date.now()}`
  const fileName = `note-${Date.now()}.txt`
  watchedFolder = `${TESTS_DIR}/${folderName}`
  await ensureFolder(request, webDavUrl, accessToken, watchedFolder)
  await uploadTextFile(
    request,
    webDavUrl,
    accessToken,
    `${watchedFolder}/${fileName}`,
    'Watch smoke test: Synaplan keeps this sentence searchable.'
  )

  await navigateToFolder(userPage, TESTS_DIR)
  const row = await findFileRow(userPage, folderName)
  await openSynaplanMenu(row)
  await userPage.locator('.oc-files-actions-watch-trigger').click()

  const dialog = userPage.locator('[data-testid="synaplan-watch-dialog"]')
  await expect(dialog).toBeVisible({ timeout: 10_000 })

  // Taggable oc-select: type with real keystrokes, then pick the create
  // option vue-select offers (see knowledge.spec.ts).
  const picker = userPage.locator('[data-testid="synaplan-watch-folder"]')
  await picker.locator('.vs__dropdown-toggle').click()
  await userPage.keyboard.type(WATCH_GROUP)
  await picker.locator('.vs__dropdown-option').first().click()

  const submit = userPage.locator('[data-testid="synaplan-watch-submit"]')
  await expect(submit).toBeEnabled({ timeout: 5_000 })
  await submit.click()
  await expect(userPage.locator('[data-testid="synaplan-watch-watching"]')).toBeVisible({
    timeout: 15_000
  })

  await userPage.locator('[data-testid="synaplan-watch-check"]').click()
  await expect(userPage.locator('[data-testid="synaplan-watch-checked"]')).toBeVisible({
    timeout: 60_000
  })
  await expect(userPage.locator('[data-testid="synaplan-watch-error"]')).toBeHidden()

  // The file is where the person will look for it: in the knowledge folder.
  await expect
    .poll(() => knowledgeFileNames(request, SYNAPLAN_API_URL, synaplanToken, WATCH_GROUP), {
      timeout: 15_000
    })
    .toContain(fileName)

  await userPage.locator('[data-testid="synaplan-watch-stop"]').click()
  await expect(userPage.locator('[data-testid="synaplan-watch-stopped"]')).toBeVisible({
    timeout: 15_000
  })

  await userPage.locator('.oc-modal-title-actions-cancel').click()
  await expect(dialog).toBeHidden()
})

test('a watch that cannot load shows one sentence', async ({ request }) => {
  watchedFolder = `${TESTS_DIR}/watch-fail-${Date.now()}`
  await ensureFolder(request, webDavUrl, accessToken, watchedFolder)

  await userPage.route('**/api/synaplan/watches/options*', (route) =>
    route.fulfill({ status: 502, contentType: 'application/json', body: '{}' })
  )

  await navigateToFolder(userPage, TESTS_DIR)
  const row = await findFileRow(userPage, watchedFolder.split('/')[1])
  await openSynaplanMenu(row)
  await userPage.locator('.oc-files-actions-watch-trigger').click()

  const error = userPage.locator('[data-testid="synaplan-watch-error"]')
  await expect(error).toBeVisible({ timeout: 10_000 })
  await expect(error).toHaveText(
    'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
  )
})

test('watch a file, check now, the file is in Synaplan, stop', async ({ request }) => {
  test.setTimeout(90_000)

  const fileName = `watched-${Date.now()}.txt`
  watchedFolder = `${TESTS_DIR}/watch-file-${Date.now()}`
  watchedFile = `${watchedFolder}/${fileName}`
  await ensureFolder(request, webDavUrl, accessToken, watchedFolder)
  await uploadTextFile(
    request,
    webDavUrl,
    accessToken,
    watchedFile,
    'Watch smoke test: Synaplan keeps this file searchable.'
  )

  await navigateToFolder(userPage, TESTS_DIR)
  await (await findFileRow(userPage, watchedFolder.split('/')[1])).click()
  const row = await findFileRow(userPage, fileName)
  await openSynaplanMenu(row)
  await userPage.locator('.oc-files-actions-watch-trigger').click()

  const dialog = userPage.locator('[data-testid="synaplan-watch-dialog"]')
  await expect(dialog).toBeVisible({ timeout: 10_000 })
  await expect(dialog).toContainText('What should happen when this file changes?')

  const picker = userPage.locator('[data-testid="synaplan-watch-folder"]')
  await picker.locator('.vs__dropdown-toggle').click()
  await userPage.keyboard.type(WATCH_GROUP)
  await picker.locator('.vs__dropdown-option').first().click()

  const submit = userPage.locator('[data-testid="synaplan-watch-submit"]')
  await expect(submit).toBeEnabled({ timeout: 5_000 })
  await submit.click()
  await expect(userPage.locator('[data-testid="synaplan-watch-watching"]')).toBeVisible({
    timeout: 15_000
  })

  await userPage.locator('[data-testid="synaplan-watch-check"]').click()
  await expect(userPage.locator('[data-testid="synaplan-watch-checked"]')).toBeVisible({
    timeout: 60_000
  })
  await expect(userPage.locator('[data-testid="synaplan-watch-error"]')).toBeHidden()

  await expect
    .poll(() => knowledgeFileNames(request, SYNAPLAN_API_URL, synaplanToken, WATCH_GROUP), {
      timeout: 15_000
    })
    .toContain(fileName)

  await userPage.locator('[data-testid="synaplan-watch-stop"]').click()
  await expect(userPage.locator('[data-testid="synaplan-watch-stopped"]')).toBeVisible({
    timeout: 15_000
  })
})
