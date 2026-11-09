import { test, Page, expect } from '@playwright/test'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import {
  allowBackgroundWork,
  connection,
  stopBackgroundWork
} from '../../support/helpers/cloudHelper'

/**
 * Allow Synaplan to work while away: the Synaplan page gets an
 * authorization code for the service's own client silently from the
 * identity provider, the service redeems it for an offline token and
 * binds the login in Synaplan with the person's own token (no instance
 * secret). Stop forgets the token; allowing again works without a prompt.
 */

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'

test.describe.configure({ mode: 'serial' })

let userPage: Page

test.beforeEach(async ({ browser }) => {
  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
})

test.afterEach(async () => {
  await logout(userPage).catch(() => {})
})

test('allow, stop and allow again work while away', async ({ request }) => {
  test.setTimeout(60_000)
  const token = await getAccessToken(request, TEST_USER, TEST_PASSWORD)

  await stopBackgroundWork(userPage)
  expect((await connection(request, token)).background).toBe(false)

  await allowBackgroundWork(userPage)
  await expect(userPage.locator('[data-testid="synaplan-background-state"]')).toContainText(
    'while you are away'
  )
  const connected = await connection(request, token)
  expect(connected.background).toBe(true)
  expect(connected.bound).toBe(true)

  await stopBackgroundWork(userPage)
  expect((await connection(request, token)).background).toBe(false)

  await allowBackgroundWork(userPage)
  expect((await connection(request, token)).background).toBe(true)
  await expect(userPage.locator('[data-testid="synaplan-background-error"]')).toHaveCount(0)
})
