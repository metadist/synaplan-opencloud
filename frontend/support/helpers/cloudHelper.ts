import { APIRequestContext, Page, expect } from '@playwright/test'
import { getAccessToken } from './authHelper'

const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'

/**
 * Register this OpenCloud with Synaplan the way an administrator does it,
 * from the Synaplan page's backend route. The realm's `admin` is an
 * administrator in both, so the instance is active at once. Idempotent.
 */
export async function ensureRegistered(request: APIRequestContext): Promise<void> {
  const token = await getAccessToken(request, 'admin', 'admin')
  const resp = await request.post(`${OC_API_URL}/api/synaplan/instance/register`, {
    headers: { Authorization: `Bearer ${token}` },
    data: {}
  })
  expect(resp.ok(), await resp.text()).toBe(true)
  expect((await resp.json()).status).toBe('active')
}

/** Allow Synaplan to work while away, from the Synaplan page. */
export async function allowBackgroundWork(page: Page): Promise<void> {
  await page.goto('/synaplan')
  const state = page.locator('[data-testid="synaplan-background-state"]')
  await expect(state).toBeVisible({ timeout: 15_000 })
  const allow = page.locator('[data-testid="synaplan-background-connect"]')
  if (await allow.isVisible()) {
    await allow.click()
  }
  await expect(page.locator('[data-testid="synaplan-background-disconnect"]')).toBeVisible({
    timeout: 20_000
  })
}

/** Stop background work, from the Synaplan page. */
export async function stopBackgroundWork(page: Page): Promise<void> {
  await page.goto('/synaplan')
  const stop = page.locator('[data-testid="synaplan-background-disconnect"]')
  await expect(stop.or(page.locator('[data-testid="synaplan-background-connect"]'))).toBeVisible({
    timeout: 15_000
  })
  if (await stop.isVisible()) {
    await stop.click()
  }
  await expect(page.locator('[data-testid="synaplan-background-connect"]')).toBeVisible({
    timeout: 15_000
  })
}

/** The connection as the Go service sees it for the token's person. */
export async function connection(
  request: APIRequestContext,
  token: string
): Promise<{ bound: boolean; background: boolean }> {
  const resp = await request.get(`${OC_API_URL}/api/synaplan/connection`, {
    headers: { Authorization: `Bearer ${token}` }
  })
  expect(resp.ok(), await resp.text()).toBe(true)
  return resp.json()
}
