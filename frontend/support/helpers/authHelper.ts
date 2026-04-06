import { APIRequestContext, Browser, Page } from '@playwright/test'
import { LoginPage } from '../pages/loginPage'
import { createContext, closeContext } from './actorHelper'

/**
 * Get an OIDC access token from Keycloak via the Resource Owner
 * Password grant. Used by E2E tests to call OpenCloud's graph /
 * WebDAV APIs directly (e.g. for file fixture setup) without going
 * through the full UI login flow.
 *
 * `scope=openid` is mandatory — without it Keycloak hands back a token
 * OC's proxy rejects with 401.
 */
export async function getAccessToken(
  request: APIRequestContext,
  username: string,
  password: string,
  opts: {
    keycloakUrl?: string
    realm?: string
    clientId?: string
  } = {}
): Promise<string> {
  // Local dev: synaplan's `docker compose --profile oidc` publishes
  // Keycloak on :8443. CI uses the test stack which publishes on :8444
  // and sets KEYCLOAK_HTTPS_URL — pick that up when present so the
  // same helper works in both environments without an explicit opt.
  const keycloakUrl =
    opts.keycloakUrl ?? process.env.KEYCLOAK_HTTPS_URL ?? 'https://host.docker.internal:8443'
  const realm = opts.realm ?? 'synaplan'
  const clientId = opts.clientId ?? 'opencloud'

  const resp = await request.post(`${keycloakUrl}/realms/${realm}/protocol/openid-connect/token`, {
    form: {
      grant_type: 'password',
      client_id: clientId,
      scope: 'openid',
      username,
      password
    }
  })
  if (!resp.ok()) {
    throw new Error(`keycloak token request failed: ${resp.status()} ${await resp.text()}`)
  }
  const body = (await resp.json()) as { access_token?: string }
  if (!body.access_token) {
    throw new Error('keycloak response missing access_token')
  }
  return body.access_token
}

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
