import { test, expect, APIRequestContext, Page } from '@playwright/test'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import { openSynaplanMenu } from '../../support/helpers/contextMenuHelper'
import { allowWhileAway, stopWhileAway } from '../../support/helpers/delegationHelper'
import {
  deleteFileQuiet,
  ensureFolder,
  getPersonalSpace,
  getResourceId,
  uploadTextFile
} from '../../support/helpers/filesHelper'
import {
  knowledgeFileNames,
  knowledgeTextPreview,
  wipeKnowledgeGroup
} from '../../support/helpers/synaplanApiHelper'

/**
 * Keeping a searchable file up to date: the person asks for it in the
 * knowledge dialog, and the service follows later edits. OpenCloud does not
 * tell an extension about changes, so the service checks the kept files in
 * its background pass; these journeys run the same check at once through
 * POST /api/synaplan/knowledge/sync. Edits only follow where the service
 * reads files while the person is away (they allowed it, and OpenCloud's
 * machine auth key is set); elsewhere the dialog says why they will not.
 */

const TESTS_DIR = 'synaplan-e2e'
const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const GROUP = 'OC_E2E_KEEP'

// Serial: the tests share GROUP and the person's permission to work while away.
test.describe.configure({ mode: 'serial' })

const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const SYNAPLAN_API_URL = process.env.SYNAPLAN_API_URL ?? 'http://host.docker.internal:8000'

let userPage: Page
let webDavUrl: string
let accessToken: string
let synaplanToken: string
const filesToCleanup: string[] = []

test.beforeEach(async ({ browser, request }) => {
  accessToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  webDavUrl = (await getPersonalSpace(request, OC_API_URL, accessToken)).webDavUrl
  filesToCleanup.length = 0
  await ensureFolder(request, webDavUrl, accessToken, TESTS_DIR)
  synaplanToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD, {
    clientId: 'synaplan-app',
    clientSecret: 'test-oidc-secret'
  })
  await wipeKnowledgeGroup(request, SYNAPLAN_API_URL, synaplanToken, GROUP)
  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
  await allowWhileAway(userPage)
})

test.afterEach(async ({ request }) => {
  for (const path of filesToCleanup) {
    await deleteFileQuiet(request, webDavUrl, accessToken, path)
  }
  await wipeKnowledgeGroup(request, SYNAPLAN_API_URL, synaplanToken, GROUP).catch(() => {})
  if (userPage) {
    await userPage.keyboard.press('Escape').catch(() => {})
    await stopWhileAway(userPage).catch(() => {})
    await logout(userPage).catch(() => {})
  }
})

test('an edit of a kept file reaches Synaplan', async ({ request }) => {
  test.setTimeout(120_000)
  const name = `keep-${Date.now()}.txt`
  const path = `${TESTS_DIR}/${name}`
  await uploadTextFile(request, webDavUrl, accessToken, path, 'First version of the kept file.')
  filesToCleanup.push(path)

  const follows = await editsCanFollow(request, path)
  await addKept(userPage, name)
  const kept = userPage.locator('[data-testid="synaplan-knowledge-kept"]')
  const notKept = userPage.locator('[data-testid="synaplan-knowledge-not-kept"]')
  await expect(follows ? kept : notKept).toBeVisible()
  await closeDialog(userPage)
  test.skip(!follows, 'This OpenCloud does not read files while the person is away.')

  await expect.poll(() => preview(request, name), { timeout: 15_000 }).toContain('First version')

  await uploadTextFile(request, webDavUrl, accessToken, path, 'Second version, after an edit.')
  const result = await syncNow(request)
  expect(result.changed).toBeGreaterThanOrEqual(1)
  expect(result.failed).toBe(0)

  await expect.poll(() => preview(request, name), { timeout: 30_000 }).toContain('Second version')
})

test('a kept file deleted in OpenCloud leaves the knowledge folder', async ({ request }) => {
  test.setTimeout(120_000)
  const name = `keep-gone-${Date.now()}.txt`
  const path = `${TESTS_DIR}/${name}`
  await uploadTextFile(request, webDavUrl, accessToken, path, 'This file will be deleted.')
  filesToCleanup.push(path)

  const follows = await editsCanFollow(request, path)
  test.skip(!follows, 'This OpenCloud does not read files while the person is away.')
  await addKept(userPage, name)
  await expect(userPage.locator('[data-testid="synaplan-knowledge-kept"]')).toBeVisible()
  await closeDialog(userPage)
  await expect
    .poll(() => knowledgeFileNames(request, SYNAPLAN_API_URL, synaplanToken, GROUP), {
      timeout: 15_000
    })
    .toContain(name)

  await deleteFileQuiet(request, webDavUrl, accessToken, path)
  const result = await syncNow(request)
  expect(result.deleted).toBeGreaterThanOrEqual(1)

  await expect
    .poll(() => knowledgeFileNames(request, SYNAPLAN_API_URL, synaplanToken, GROUP), {
      timeout: 30_000
    })
    .not.toContain(name)
})

// Whether this service reads the person's files while they are away, as the
// watch dialog learns it.
async function editsCanFollow(request: APIRequestContext, path: string): Promise<boolean> {
  const targetId = await getResourceId(request, webDavUrl, accessToken, path)
  const resp = await request.get(`${OC_API_URL}/api/synaplan/watches/options`, {
    params: { targetId },
    headers: { Authorization: `Bearer ${accessToken}` }
  })
  expect(resp.ok(), await resp.text()).toBe(true)
  return (await resp.json()).uploadsEnabled === true
}

async function syncNow(
  request: APIRequestContext
): Promise<{ changed: number; deleted: number; failed: number }> {
  const resp = await request.post(`${OC_API_URL}/api/synaplan/knowledge/sync`, {
    headers: { Authorization: `Bearer ${accessToken}` }
  })
  expect(resp.ok(), await resp.text()).toBe(true)
  return resp.json()
}

function preview(request: APIRequestContext, name: string) {
  return knowledgeTextPreview(request, SYNAPLAN_API_URL, synaplanToken, GROUP, name).then(
    (text) => text ?? ''
  )
}

async function addKept(page: Page, name: string) {
  await page.goto('/', { waitUntil: 'domcontentloaded' })
  const folder = await findRow(page, TESTS_DIR)
  await folder.click()
  const row = await findRow(page, name)
  await openSynaplanMenu(row)
  await page.locator('.oc-files-actions-add-to-knowledge-trigger').click()
  await expect(page.locator('[data-testid="synaplan-knowledge-dialog"]')).toBeVisible({
    timeout: 10_000
  })

  // Taggable oc-select: type with real keystrokes, then pick the create
  // option vue-select offers (see knowledge.spec.ts).
  const picker = page.locator('[data-testid="synaplan-knowledge-group"]')
  await picker.locator('.vs__dropdown-toggle').click()
  await page.keyboard.type(GROUP)
  await picker.locator('.vs__dropdown-option').first().click()

  await expect(page.locator('[data-testid="synaplan-knowledge-keep"] input')).toBeChecked()
  const submit = page.locator('[data-testid="synaplan-knowledge-submit"]')
  await expect(submit).toBeEnabled({ timeout: 5_000 })
  await submit.click()
  await expect(page.locator('[data-testid="synaplan-knowledge-success"]')).toBeVisible({
    timeout: 30_000
  })
}

async function closeDialog(page: Page) {
  await page.locator('.oc-modal-title-actions-cancel').click()
  await expect(page.locator('[data-testid="synaplan-knowledge-dialog"]')).toBeHidden()
}

async function findRow(page: Page, name: string) {
  const row = page.locator(
    `#files-space-table [data-test-resource-name="${name}"], #tiles-view [data-test-resource-name="${name}"]`
  )
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  return row
}
