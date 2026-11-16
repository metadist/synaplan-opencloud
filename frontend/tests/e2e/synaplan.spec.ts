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

test('ask Synaplan a question in general on the Synaplan page', async () => {
  test.setTimeout(90_000)
  await userPage.goto('/synaplan')
  await expect(userPage.locator('[data-testid="synaplan-ask"]')).toBeVisible()

  const submit = userPage.locator('[data-testid="synaplan-ask-submit"]')
  await expect(submit).toBeDisabled()
  await userPage.locator('[data-testid="synaplan-ask-question"]').fill('Say hello.')
  await submit.click()

  const result = userPage.locator('[data-testid="synaplan-ask-result"]')
  await expect(result).toBeVisible({ timeout: 60_000 })
  await expect(result).not.toBeEmpty()
  await expect(userPage.locator('[data-testid="synaplan-ask-error"]')).toBeHidden()
})
