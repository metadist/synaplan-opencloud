import { test, expect, Page, APIRequestContext } from '@playwright/test'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'

/**
 * The administrator's settings on the Synaplan page: what Synaplan offers
 * this OpenCloud, the connection mode, and the answer language. Only an
 * OpenCloud administrator (Keycloak's `admin`, OpenCloud's Admin role) may
 * change the language; everyone else reads it. The settings live in
 * OpenCloud's NATS, so every test starts from and leaves the defaults.
 */

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const ADMIN_USER = 'admin'
const ADMIN_PASSWORD = 'admin'
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'

const DEFAULTS = { defaultAnswerLanguage: 'en', useUserLanguage: true }

test.describe.configure({ mode: 'serial' })

let page: Page | undefined

async function saveByApi(
  request: APIRequestContext,
  username: string,
  password: string,
  data: object
) {
  const token = await getAccessToken(request, username, password)
  return request.put(`${OC_API_URL}/api/synaplan/settings`, {
    headers: { Authorization: `Bearer ${token}` },
    data
  })
}

test.beforeEach(async ({ request }) => {
  const resp = await saveByApi(request, ADMIN_USER, ADMIN_PASSWORD, DEFAULTS)
  expect(resp.status()).toBe(200)
})

test.afterEach(async ({ request }) => {
  if (page) {
    await logout(page).catch(() => {})
    page = undefined
  }
  await saveByApi(request, ADMIN_USER, ADMIN_PASSWORD, DEFAULTS)
})

test('a person sees what Synaplan offers and the answer language, read-only', async ({
  browser
}) => {
  page = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
  await page.goto('/synaplan')

  await expect(page.locator('[data-testid="synaplan-admin-instance"]')).toContainText(
    'Synaplan answers',
    { timeout: 15_000 }
  )
  await expect(page.locator('[data-testid="synaplan-admin-mode"]')).toContainText(
    'organisation login'
  )
  await expect(page.locator('[data-testid="synaplan-admin-readonly"]')).toContainText(
    'otherwise in English'
  )
  await expect(page.locator('[data-testid="synaplan-admin-save"]')).toHaveCount(0)
})

test('a person cannot change the answer language', async ({ request }) => {
  const resp = await saveByApi(request, TEST_USER, TEST_PASSWORD, {
    defaultAnswerLanguage: 'de',
    useUserLanguage: false
  })
  expect(resp.status()).toBe(403)
  expect(await resp.json()).toMatchObject({
    code: 'admin_only',
    error: 'Only an administrator can change this.'
  })
})

test('the administrator saves the answer language and people read it', async ({ browser }) => {
  test.setTimeout(60_000)
  page = (await loginAsUser(browser, ADMIN_USER, ADMIN_PASSWORD)).page
  await page.goto('/synaplan')
  await expect(page.locator('[data-testid="synaplan-admin-readonly"]')).toHaveCount(0)

  await page
    .locator('[data-testid="synaplan-admin-language"] .vs__dropdown-toggle')
    .first()
    .click({ timeout: 15_000 })
  await page.getByRole('option', { name: 'German' }).click()
  await page
    .locator('[data-testid="synaplan-admin-user-language"] input[type="checkbox"]')
    .uncheck()
  await page.locator('[data-testid="synaplan-admin-save"]').click()
  await expect(page.locator('[data-testid="synaplan-admin-saved"]')).toBeVisible({
    timeout: 15_000
  })
  await logout(page)

  page = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
  await page.goto('/synaplan')
  await expect(page.locator('[data-testid="synaplan-admin-readonly"]')).toHaveText(
    'Answers come in German.',
    { timeout: 15_000 }
  )
})
