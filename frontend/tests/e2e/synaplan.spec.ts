import { test, Page, expect } from '@playwright/test'
import { loginAsUser, logout } from '../../support/helpers/authHelper'

let userPage: Page

test.beforeEach(async ({ browser }) => {
  userPage = (await loginAsUser(browser, 'testuser@synaplan.com', 'testpass123')).page
})

test.afterEach(async () => {
  await logout(userPage)
})

test('in-app synaplan view test connection succeeds', async () => {
  // Reach the view directly. When synaplanUrl is set, the app-switcher
  // item opens that URL in a new tab instead of this page.
  await userPage.goto('/synaplan')
  await expect(userPage.locator('[data-testid="synaplan-title"]')).toBeVisible()

  await userPage.locator('[data-testid="synaplan-test-btn"]').click()

  const account = userPage.locator('[data-testid="synaplan-account"]')
  await expect(account).toBeVisible({ timeout: 15_000 })
  await expect(account).toContainText('testuser@synaplan.com')
})

test('the Synaplan page answers who is connected and what Synaplan may read', async () => {
  await userPage.goto('/synaplan')
  await expect(userPage.locator('[data-testid="synaplan-title"]')).toBeVisible()

  // The account card: connection state plus the read scope the person chose.
  await expect(userPage.locator('[data-testid="synaplan-connection"]')).toBeVisible({
    timeout: 15_000
  })
  await expect(userPage.locator('[data-testid="synaplan-settings"]')).toBeVisible({
    timeout: 15_000
  })

  // A regular user sees the administrator's choices, read-only.
  const admin = userPage.locator('[data-testid="synaplan-admin"]')
  await expect(admin).toBeVisible({ timeout: 15_000 })
  await expect(userPage.locator('[data-testid="synaplan-admin-readonly"]')).toBeVisible()
  await expect(userPage.locator('[data-testid="synaplan-admin-save"]')).toHaveCount(0)
})
