import { type Locator, type Page } from '@playwright/test'

/**
 * Locate a resource row in the files table or tiles view by the
 * standard `data-test-resource-name` attribute.
 */
export async function findFileRow(page: Page, name: string): Promise<Locator> {
  const row = page.locator(
    `#files-space-table [data-test-resource-name="${name}"], #tiles-view [data-test-resource-name="${name}"]`
  )
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  return row
}

/**
 * Reload the files view so freshly-uploaded fixtures appear, then
 * open the named folder.
 */
export async function navigateToFolder(page: Page, name: string): Promise<void> {
  await page.reload({ waitUntil: 'domcontentloaded' })
  const folderRow = await findFileRow(page, name)
  await folderRow.click()
}
