import { test, expect, Page } from '@playwright/test'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import { deleteFileQuiet, downloadFile, getPersonalSpace } from '../../support/helpers/filesHelper'
import { deleteSynaplanFileQuiet, storeSynaplanFile } from '../../support/helpers/synaplanApiHelper'

/**
 * An image made in Synaplan, saved into the person's files under
 * Synaplan/Images. Synaplan's test stack has no model that makes a real
 * image, so the save journey stores an image in Synaplan first and lets
 * the page's generate call name it; the save itself runs through the
 * proxy, the Go service, Synaplan and CS3. The second journey makes the
 * real generate call and accepts either terminal state: a saved image, or
 * one readable sentence.
 */

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const SYNAPLAN_API_URL = process.env.SYNAPLAN_API_URL ?? 'http://host.docker.internal:8000'

const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==',
  'base64'
)

test.describe.configure({ mode: 'serial' })

let userPage: Page
let accessToken: string
let synaplanToken: string
let webDavUrl: string
let synaplanFileId = 0
const filesToCleanup: string[] = []

test.beforeEach(async ({ browser, request }) => {
  accessToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  synaplanToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD, {
    clientId: 'synaplan-app',
    clientSecret: 'test-oidc-secret'
  })
  webDavUrl = (await getPersonalSpace(request, OC_API_URL, accessToken)).webDavUrl
  filesToCleanup.length = 0
  synaplanFileId = 0
  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
})

test.afterEach(async ({ request }) => {
  for (const path of filesToCleanup) {
    await deleteFileQuiet(request, webDavUrl, accessToken, path)
  }
  if (synaplanFileId) {
    await deleteSynaplanFileQuiet(request, SYNAPLAN_API_URL, synaplanToken, synaplanFileId)
  }
  if (userPage) {
    await logout(userPage).catch(() => {})
  }
})

test('an image made in Synaplan is saved into the files under Synaplan/Images', async ({
  request
}) => {
  test.setTimeout(60_000)
  synaplanFileId = await storeSynaplanFile(
    request,
    SYNAPLAN_API_URL,
    synaplanToken,
    `e2e-image-${Date.now()}.png`,
    PNG,
    'image/png'
  )
  await userPage.route('**/api/synaplan/media', (route) =>
    route.fulfill({
      json: { url: `/api/v1/files/${synaplanFileId}/download`, mimeType: 'image/png' }
    })
  )

  const prompt = `e2e red square ${Date.now()}`
  await userPage.goto('/synaplan')
  await userPage.locator('[data-testid="synaplan-image-prompt"]').fill(prompt)
  await userPage.locator('[data-testid="synaplan-image-make"]').click()

  const savedPath = `Synaplan/Images/${prompt}.png`
  await expect(userPage.locator('[data-testid="synaplan-image-saved"]')).toContainText(savedPath, {
    timeout: 30_000
  })
  filesToCleanup.push(savedPath)
  await expect(userPage.locator('[data-testid="synaplan-image-error"]')).toHaveCount(0)

  const saved = await downloadFile(request, webDavUrl, accessToken, savedPath)
  expect(saved.equals(PNG)).toBe(true)
})

test('making an image ends in a saved image or one readable sentence', async ({ request }) => {
  test.setTimeout(150_000)
  const prompt = `e2e blue circle ${Date.now()}`
  await userPage.goto('/synaplan')
  await userPage.locator('[data-testid="synaplan-image-prompt"]').fill(prompt)
  const make = userPage.locator('[data-testid="synaplan-image-make"]')
  await make.click()

  const saved = userPage.locator('[data-testid="synaplan-image-saved"]')
  const failed = userPage.locator('[data-testid="synaplan-image-error"]')
  await expect(saved.or(failed)).toBeVisible({ timeout: 120_000 })
  await expect(make).toBeEnabled()

  if (await saved.isVisible()) {
    const text = (await saved.textContent()) ?? ''
    const path = /Saved to (.+)\. The image is still in Synaplan\./.exec(text)?.[1]
    expect(path).toMatch(/^Synaplan\/Images\//)
    filesToCleanup.push(path as string)
    const bytes = await downloadFile(request, webDavUrl, accessToken, path as string)
    expect(bytes.length).toBeGreaterThan(0)
  } else {
    await expect(failed).not.toContainText('status code')
    await expect(failed).toHaveText(/\.$/)
  }
})
