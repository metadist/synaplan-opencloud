import { Browser, Page } from '@playwright/test'
import { LoginPage } from '../pages/loginPage'
import { createContext, closeContext } from './actorHelper'

export async function loginAsUser(
  browser: Browser,
  username: string,
  password: string
): Promise<{ page: Page }> {
  const { page } = await createContext(browser)
  await page.goto('/', { waitUntil: 'domcontentloaded' })

  // OpenCloud SPA loads, then redirects to Keycloak login form
  const loginPage = new LoginPage(page)
  await loginPage.usernameField.waitFor({ state: 'visible', timeout: 30_000 })

  // Fill credentials and wait for the /token response (OIDC code→token exchange)
  await Promise.all([
    page.waitForResponse(
      (resp) =>
        resp.url().includes('/token') && resp.status() === 200 && resp.request().method() === 'POST'
    ),
    loginPage.login(username, password)
  ])

  // Wait for OpenCloud to load after OIDC callback
  await page
    .waitForSelector('[data-testid="app-loading-spinner"]', { state: 'hidden', timeout: 15_000 })
    .catch(() => {})
  await page.getByLabel('Application Switcher').waitFor({ state: 'visible', timeout: 15_000 })

  return { page }
}

export async function logout(page: Page): Promise<void> {
  const context = page.context()
  await page.locator('#_userMenuButton').click()
  await page.locator('#oc-topbar-account-logout').click()
  await closeContext(context)
}
