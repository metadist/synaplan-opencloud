import { test, Page, expect } from '@playwright/test'
import { loginAsUser, logout } from '../../support/helpers/authHelper'

let userPage: Page

test.beforeEach(async ({ browser }) => {
  userPage = (await loginAsUser(browser, 'testuser@synaplan.com', 'testpass123')).page
})

test.afterEach(async () => {
  await logout(userPage)
})

async function navigateToSynaplanApp(page: Page) {
  const appSwitcher = page.getByLabel('Application Switcher')
  await appSwitcher.click()

  const synaplanApp = page.locator('[data-test-id="app.synaplan.menuItem"]')
  await expect(synaplanApp).toBeVisible()
  await synaplanApp.click()

  await expect(page.locator('[data-testid="synaplan-title"]')).toBeVisible()
}

test('synaplan app is accessible from app switcher', async () => {
  await navigateToSynaplanApp(userPage)
  await expect(userPage.locator('[data-testid="synaplan-title"]')).toBeVisible()
})

test('test connection succeeds', async () => {
  await navigateToSynaplanApp(userPage)

  const testBtn = userPage.locator('[data-testid="synaplan-test-btn"]')
  await testBtn.click()

  const result = userPage.locator('[data-testid="synaplan-result"]')
  await expect(result).toBeVisible({ timeout: 15_000 })
  await expect(result).toContainText('"status": "ok"')
})
