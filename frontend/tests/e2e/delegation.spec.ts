import { APIRequestContext, Page, expect, test } from '@playwright/test'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'

/**
 * Working while the person is away: on the Synaplan page the person allows
 * it, the browser gets an authorization code for the service's own client
 * from the identity provider (silently, the person is signed in there), and
 * the service redeems it for an offline token. Stop revokes that token at
 * the identity provider; allowing again works without a prompt.
 */

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const SYNAPLAN_API_URL = process.env.SYNAPLAN_API_URL ?? 'http://host.docker.internal:8000'

test.describe.configure({ mode: 'serial' })

let userPage: Page

test.beforeEach(async ({ browser }) => {
  userPage = (await loginAsUser(browser, TEST_USER, TEST_PASSWORD)).page
})

test.afterEach(async () => {
  await stop(userPage).catch(() => {})
  await logout(userPage).catch(() => {})
})

async function connected(request: APIRequestContext, token: string): Promise<boolean> {
  const resp = await request.get(`${OC_API_URL}/api/synaplan/delegation`, {
    headers: { Authorization: `Bearer ${token}` }
  })
  expect(resp.ok(), await resp.text()).toBe(true)
  return (await resp.json()).connected
}

async function openSection(page: Page) {
  if (!new URL(page.url()).pathname.endsWith('/synaplan')) {
    await page.goto('/synaplan')
  }
  const connect = page.locator('[data-testid="synaplan-delegation-connect"]')
  const disconnect = page.locator('[data-testid="synaplan-delegation-disconnect"]')
  await expect(connect.or(disconnect)).toBeVisible({ timeout: 15_000 })
  return { connect, disconnect }
}

async function allow(page: Page): Promise<void> {
  const { connect, disconnect } = await openSection(page)
  if (await connect.isVisible()) {
    await connect.click()
  }
  await expect(disconnect).toBeVisible({ timeout: 20_000 })
}

async function stop(page: Page): Promise<void> {
  const { connect, disconnect } = await openSection(page)
  if (await disconnect.isVisible()) {
    await disconnect.click()
  }
  await expect(connect).toBeVisible({ timeout: 15_000 })
}

test('allow, stop and allow again to work while away', async ({ request }) => {
  test.setTimeout(60_000)
  const token = await getAccessToken(request, TEST_USER, TEST_PASSWORD)

  await stop(userPage)
  expect(await connected(request, token)).toBe(false)

  await allow(userPage)
  await expect(userPage.locator('[data-testid="synaplan-delegation-state"]')).toContainText(
    'while you are away'
  )
  expect(await connected(request, token)).toBe(true)

  await stop(userPage)
  expect(await connected(request, token)).toBe(false)

  await allow(userPage)
  expect(await connected(request, token)).toBe(true)
  await expect(userPage.locator('[data-testid="synaplan-delegation-error"]')).toHaveCount(0)
})

// The sign-in service may need the person (a login, a consent): the silent
// attempt then comes back empty, and a popup the person opens with a second
// click finishes it. Aborting the silent request makes that happen here.
test('when the silent attempt fails, a popup the person opens allows it', async ({ request }) => {
  test.setTimeout(60_000)
  const token = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  await stop(userPage)
  await userPage.route(/\/protocol\/openid-connect\/auth\?.*prompt=none/, (route) => route.abort())
  const { connect, disconnect } = await openSection(userPage)

  await connect.click()
  const signIn = userPage.locator('[data-testid="synaplan-delegation-sign-in"]')
  await expect(signIn).toBeVisible({ timeout: 20_000 })
  const [popup] = await Promise.all([userPage.waitForEvent('popup'), signIn.click()])
  await popup.waitForEvent('close', { timeout: 20_000 })

  await expect(disconnect).toBeVisible({ timeout: 15_000 })
  expect(await connected(request, token)).toBe(true)
  await userPage.unrouteAll()
})

test('a refused permission shows one readable sentence', async ({ request }) => {
  test.setTimeout(60_000)
  const token = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  const { connect } = await openSection(userPage)
  await userPage.route('**/api/synaplan/delegation', (route) =>
    route.request().method() === 'POST'
      ? route.fulfill({ status: 400, json: { error: 'The sign-in answer is incomplete.' } })
      : route.fallback()
  )

  await connect.click()

  await expect(userPage.locator('[data-testid="synaplan-delegation-error"]')).toHaveText(
    'Synaplan could not be allowed to work for you. Try again.',
    { timeout: 20_000 }
  )
  expect(await connected(request, token)).toBe(false)
  await userPage.unroute('**/api/synaplan/delegation')
})

// Synaplan's side: the person ends the link in Synaplan, Synaplan sends
// identity_revoked to this OpenCloud, and the service forgets the person's
// offline token. Crosses Synaplan's signed notification, its published
// issuer and keys, and the inbound route. Runs as demo: Synaplan then
// refuses that person through this OpenCloud, which the other journeys
// must not inherit, and never links them again, so it needs a fresh
// Synaplan test stack, as CI starts one.
test('ending the link in Synaplan ends work while away here', async ({ browser, request }) => {
  test.setTimeout(60_000)
  const demoPage = (await loginAsUser(browser, 'demo', 'demo')).page
  const token = await getAccessToken(request, 'demo', 'demo')
  const me = await request.get(`${OC_API_URL}/api/synaplan/me`, {
    headers: { Authorization: `Bearer ${token}` }
  })
  expect(me.ok(), await me.text()).toBe(true)
  await allow(demoPage)
  expect(await connected(request, token)).toBe(true)

  const synaplanToken = await getAccessToken(request, 'demo', 'demo', {
    clientId: 'synaplan-app',
    clientSecret: 'test-oidc-secret'
  })
  const headers = { Authorization: `Bearer ${synaplanToken}` }
  const list = await request.get(`${SYNAPLAN_API_URL}/api/v1/me/platform-links`, { headers })
  expect(list.ok(), await list.text()).toBe(true)
  const links = (await list.json()).links as { id: number; client: string; status: string }[]
  const link = links.find((l) => l.client === 'opencloud' && l.status === 'active')
  expect(link, JSON.stringify(links)).toBeDefined()

  const ended = await request.delete(`${SYNAPLAN_API_URL}/api/v1/me/platform-links/${link?.id}`, {
    headers
  })
  expect(ended.ok(), await ended.text()).toBe(true)

  await expect.poll(() => connected(request, token), { timeout: 30_000 }).toBe(false)
  await logout(demoPage).catch(() => {})
})
