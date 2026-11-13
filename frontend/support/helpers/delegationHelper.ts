import { Page, expect } from '@playwright/test'

/**
 * Allow and stop Synaplan's work while the person is away, from the
 * Synaplan page, the way the person does it.
 */

async function openSection(page: Page) {
  if (!new URL(page.url()).pathname.endsWith('/synaplan')) {
    await page.goto('/synaplan')
  }
  const connect = page.locator('[data-testid="synaplan-delegation-connect"]')
  const disconnect = page.locator('[data-testid="synaplan-delegation-disconnect"]')
  await expect(connect.or(disconnect)).toBeVisible({ timeout: 15_000 })
  return { connect, disconnect }
}

export async function allowWorkWhileAway(page: Page): Promise<void> {
  const { connect, disconnect } = await openSection(page)
  if (await connect.isVisible()) {
    await connect.click()
  }
  await expect(disconnect).toBeVisible({ timeout: 20_000 })
}

export async function stopWorkWhileAway(page: Page): Promise<void> {
  const { connect, disconnect } = await openSection(page)
  if (await disconnect.isVisible()) {
    await disconnect.click()
  }
  await expect(connect).toBeVisible({ timeout: 15_000 })
}
