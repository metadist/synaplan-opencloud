import { Locator, Page, expect } from '@playwright/test'

/**
 * The "work while you are away" section on the Synaplan page. Allowing it
 * gets an authorization code from the identity provider silently (the
 * person is signed in there) and hands it to the Go service.
 */
export async function openDelegationSection(
  page: Page
): Promise<{ connect: Locator; disconnect: Locator }> {
  if (!new URL(page.url()).pathname.endsWith('/synaplan')) {
    await page.goto('/synaplan')
  }
  const connect = page.locator('[data-testid="synaplan-delegation-connect"]')
  const disconnect = page.locator('[data-testid="synaplan-delegation-disconnect"]')
  await expect(connect.or(disconnect)).toBeVisible({ timeout: 15_000 })
  return { connect, disconnect }
}

export async function allowWorkWhileAway(page: Page): Promise<void> {
  const { connect, disconnect } = await openDelegationSection(page)
  if (await connect.isVisible()) {
    await connect.click()
  }
  await expect(disconnect).toBeVisible({ timeout: 20_000 })
}

export async function stopWorkWhileAway(page: Page): Promise<void> {
  const { connect, disconnect } = await openDelegationSection(page)
  if (await disconnect.isVisible()) {
    await disconnect.click()
  }
  await expect(connect).toBeVisible({ timeout: 15_000 })
}
