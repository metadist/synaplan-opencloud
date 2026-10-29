import { test, expect, Page } from '@playwright/test'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import { openSynaplanMenu } from '../../support/helpers/contextMenuHelper'
import { deleteFileQuiet, ensureFolder, getPersonalSpace } from '../../support/helpers/filesHelper'

/**
 * Watch a folder: the action is visible on a folder, starting a watch
 * reaches "Synaplan is watching this.", the folder's Synaplan tab says
 * it is watched, and "Stop watching" undoes it from the same place.
 * The stop is also the cleanup: the journey leaves no watch behind.
 */

const TESTS_DIR = 'synaplan-e2e'
const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const KNOWLEDGE_GROUP = 'OC_E2E_TEST'

// Serial: the watch index is per person, and a parallel worker's
// stop would race this spec's start.
test.describe.configure({ mode: 'serial' })

let userPage: Page
let webDavUrl: string
let accessToken: string
let folderPath = ''

test.beforeEach(async ({ browser, request }) => {
  accessToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  const space = await getPersonalSpace(request, OC_API_URL, accessToken)
  webDavUrl = space.webDavUrl
  await ensureFolder(request, webDavUrl, accessToken, TESTS_DIR)
  folderPath = `${TESTS_DIR}/watch-${Date.now()}`
  await ensureFolder(request, webDavUrl, accessToken, folderPath)
  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
})

test.afterEach(async ({ request }) => {
  if (userPage) {
    await userPage.keyboard.press('Escape').catch(() => {})
    // Stop whatever this run started, even when an assertion failed
    // half-way, so the next run starts from a clean index.
    await userPage.request
      .delete(
        `${OC_API_URL}/api/synaplan/watches?targetId=${encodeURIComponent(await folderResourceId(request))}`,
        { headers: { Authorization: `Bearer ${accessToken}` } }
      )
      .catch(() => {})
    await logout(userPage).catch(() => {})
  }
  await deleteFileQuiet(request, webDavUrl, accessToken, folderPath)
})

test('watch context action is visible on a folder', async () => {
  await navigateToTestsFolder(userPage)
  const row = await findRow(userPage, folderName())
  await openSynaplanMenu(row)

  await expect(userPage.locator('.oc-files-actions-watch-trigger')).toBeVisible({
    timeout: 10_000
  })
  await userPage.keyboard.press('Escape')
})

test('start watching a folder, see it in the Synaplan tab, stop it there', async () => {
  test.setTimeout(90_000)

  await navigateToTestsFolder(userPage)
  const row = await findRow(userPage, folderName())
  await openSynaplanMenu(row)
  await userPage.locator('.oc-files-actions-watch-trigger').click()

  const dialog = userPage.locator('[data-testid="synaplan-watch-dialog"]')
  await expect(dialog).toBeVisible({ timeout: 10_000 })

  // Pick (or create) the knowledge folder new files should land in.
  const picker = userPage.locator('[data-testid="synaplan-watch-folder"]')
  await picker.locator('.vs__dropdown-toggle').click()
  await userPage.keyboard.type(KNOWLEDGE_GROUP)
  await picker.locator('.vs__dropdown-option').first().click()

  await userPage.locator('[data-testid="synaplan-watch-submit"]').click()
  await expect(userPage.locator('[data-testid="synaplan-watch-started"]')).toBeVisible({
    timeout: 30_000
  })
  await expect(userPage.locator('[data-testid="synaplan-watch-error"]')).toBeHidden()
  await userPage.locator('.oc-modal-title-actions-cancel').click()
  await expect(dialog).toBeHidden()

  // The folder's Synaplan tab answers "who watches this" and offers the undo.
  await openSynaplanTab(userPage, folderName())
  const watched = userPage.locator('[data-testid="synaplan-sidebar-watched"]')
  await expect(watched).toBeVisible({ timeout: 30_000 })

  await userPage.locator('[data-testid="synaplan-sidebar-stop-watching"]').click()
  await expect(userPage.locator('[data-testid="synaplan-sidebar-message"]')).toContainText(
    'Stopped',
    { timeout: 30_000 }
  )
  await expect(watched).toBeHidden({ timeout: 30_000 })
})

function folderName() {
  return folderPath.slice(TESTS_DIR.length + 1)
}

/** The resource id of the test folder, read over WebDAV for the API cleanup. */
async function folderResourceId(request: Parameters<typeof getPersonalSpace>[0]): Promise<string> {
  const url = `${webDavUrl.replace(/\/$/, '')}/${folderPath.split('/').map(encodeURIComponent).join('/')}`
  const resp = await request.fetch(url, {
    method: 'PROPFIND',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      Depth: '0',
      'Content-Type': 'application/xml'
    },
    data: '<?xml version="1.0"?><d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:prop><oc:fileid/></d:prop></d:propfind>'
  })
  const match = /<oc:fileid>([^<]+)<\/oc:fileid>/.exec(await resp.text())
  return match?.[1] ?? ''
}

async function openSynaplanTab(page: Page, name: string) {
  const row = await findRow(page, name)
  await row.click({ button: 'right' })
  await page.locator('.oc-files-actions-show-details-trigger').click()
  await page.locator('#sidebar-panel-synaplan-select').click()
  await expect(page.locator('[data-testid="synaplan-sidebar"]')).toBeVisible({ timeout: 10_000 })
}

async function findRow(page: Page, name: string) {
  const row = page.locator(
    `#files-space-table [data-test-resource-name="${name}"], #tiles-view [data-test-resource-name="${name}"]`
  )
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  return row
}

async function navigateToTestsFolder(page: Page) {
  await page.reload({ waitUntil: 'domcontentloaded' })
  const folderRow = await findRow(page, TESTS_DIR)
  await folderRow.click()
}
