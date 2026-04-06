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
  // Reach the view directly. The app-switcher menu item points at an
  // external Synaplan URL when synaplanUrl is configured (see
  // src/index.ts), so it can't be used to navigate to the internal
  // page in production-style deployments.
  await userPage.goto('/synaplan')
  await expect(userPage.locator('[data-testid="synaplan-title"]')).toBeVisible()

  await userPage.locator('[data-testid="synaplan-test-btn"]').click()

  const result = userPage.locator('[data-testid="synaplan-result"]')
  await expect(result).toBeVisible({ timeout: 15_000 })
  await expect(result).toContainText('"status": "ok"')
})
