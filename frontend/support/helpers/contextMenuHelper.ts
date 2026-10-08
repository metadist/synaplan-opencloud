import { Locator } from '@playwright/test'

export async function openSynaplanMenu(row: Locator): Promise<void> {
  await row.click({ button: 'right' })
  await row.page().locator('[id^="oc-files-context-actions-synaplan-toggle-"]').hover()
}
