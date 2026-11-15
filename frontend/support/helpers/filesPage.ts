import { type Locator, type Page } from '@playwright/test'

/** A row in the files table or the tiles view. */
export async function findFileRow(page: Page, name: string): Promise<Locator> {
  const row = page.locator(
    `#files-space-table [data-test-resource-name="${name}"], #tiles-view [data-test-resource-name="${name}"]`
  )
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  return row
}

/** Reload and open a folder by its name, the way each file journey starts. */
export async function navigateToFolder(page: Page, name: string): Promise<void> {
  await page.reload({ waitUntil: 'domcontentloaded' })
  const folderRow = await findFileRow(page, name)
  await folderRow.click()
}
