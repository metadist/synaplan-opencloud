import { Locator, Page } from '@playwright/test'

/**
 * Locate a resource row in the files list by the standard
 * `data-test-resource-name` attribute. Mirrors the selector
 * opencloud-eu/web-extensions uses in its own E2E tests.
 */
export async function findFileRow(page: Page, name: string): Promise<Locator> {
  const row = page.locator(
    `#files-space-table [data-test-resource-name="${name}"], #tiles-view [data-test-resource-name="${name}"]`
  )
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  return row
}

/**
 * Reload the files view so freshly-uploaded fixtures show up, then
 * click into the tests subdir so every test operates inside it.
 */
export async function navigateToTestsFolder(page: Page, testsDir: string): Promise<void> {
  await page.reload({ waitUntil: 'domcontentloaded' })
  const folderRow = await findFileRow(page, testsDir)
  await folderRow.click()
}
