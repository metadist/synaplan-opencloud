import { Locator, Page } from '@playwright/test'

/**
 * Login page for OpenCloud with external Keycloak IdP.
 *
 * When OpenCloud is configured with an external OIDC provider,
 * navigating to / redirects to Keycloak's login form.
 */
export class LoginPage {
  readonly page: Page
  readonly usernameField: Locator
  readonly passwordField: Locator
  readonly loginBtn: Locator

  constructor(page: Page) {
    this.page = page
    this.usernameField = this.page.getByLabel('Username or email')
    this.passwordField = this.page.locator('input[name="password"]')
    this.loginBtn = this.page.getByRole('button', { name: 'Sign In' })
  }

  async login(username: string, password: string) {
    await this.usernameField.fill(username)
    await this.passwordField.fill(password)
    await this.loginBtn.click()
  }
}
