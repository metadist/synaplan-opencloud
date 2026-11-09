import { test, expect } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import { allowBackgroundWork, ensureRegistered } from '../../support/helpers/cloudHelper'
import {
  deleteFileQuiet,
  ensureFolder,
  getPersonalSpace,
  uploadTextFile
} from '../../support/helpers/filesHelper'

/**
 * Folder read from Synaplan (pickup): Synaplan asks for a folder by its
 * path, the way a task's folder step does. The service claims the request
 * with the person's Synaplan token, resolves and lists the folder over
 * WebDAV as the person, checks the person's read setting and uploads the
 * files Synaplan accepts. Nobody is signed in while it runs.
 *
 * Synaplan opens a pickup only from a task's folder step, which needs a
 * planning model; its dev/test command `app:cloud-pickups:open-test` opens
 * the same request without one.
 */

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const TESTS_DIR = 'synaplan-e2e'
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const SYNAPLAN_CONTAINER = process.env.SYNAPLAN_APP_CONTAINER ?? 'synaplan-app-test'

test.describe.configure({ mode: 'serial' })

let ocToken: string
let webDavUrl: string
let folder = ''

function openPickup(folderPath: string): {
  state: string
  listed: number
  sent: number
  names: string[]
} {
  let out: string
  try {
    out = execFileSync('docker', [
      'exec',
      SYNAPLAN_CONTAINER,
      'php',
      'bin/console',
      'app:cloud-pickups:open-test',
      `--email=${TEST_USER}`,
      '--client=opencloud',
      `--folder=${folderPath}`,
      '--mode=fetch'
    ]).toString()
  } catch (e) {
    // A refused read exits non-zero and still prints its JSON line.
    out = String((e as { stdout?: Buffer }).stdout ?? '')
  }
  return JSON.parse(out.trim().split('\n').pop() || '{}')
}

async function setReadScope(request: Parameters<typeof getAccessToken>[0], mode: string) {
  const resp = await request.put(`${OC_API_URL}/api/synaplan/preferences`, {
    headers: { Authorization: `Bearer ${ocToken}` },
    data: { mode, folders: [], copyFolder: '' }
  })
  expect(resp.ok(), await resp.text()).toBe(true)
  expect((await resp.json()).reported).toBe(true)
}

test.beforeAll(async ({ browser, request }) => {
  await ensureRegistered(request)
  const { page } = await loginAsUser(browser, TEST_USER, TEST_PASSWORD)
  await allowBackgroundWork(page)
  await logout(page)
})

test.beforeEach(async ({ request }) => {
  ocToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  webDavUrl = (await getPersonalSpace(request, OC_API_URL, ocToken)).webDavUrl
  await ensureFolder(request, webDavUrl, ocToken, TESTS_DIR)
  folder = `${TESTS_DIR}/pickup-${Date.now()}`
  await ensureFolder(request, webDavUrl, ocToken, folder)
  await uploadTextFile(request, webDavUrl, ocToken, `${folder}/invoice-1.txt`, 'invoice one')
  await uploadTextFile(request, webDavUrl, ocToken, `${folder}/invoice-2.txt`, 'invoice two')
})

test.afterEach(async ({ request }) => {
  await setReadScope(request, 'watched').catch(() => {})
  await deleteFileQuiet(request, webDavUrl, ocToken, folder)
})

test('Synaplan reads a folder by path when the person allows all files', async ({ request }) => {
  test.setTimeout(90_000)
  await setReadScope(request, 'all')

  const result = openPickup(folder)
  expect(result.state).toBe('done')
  expect(result.sent).toBe(2)
  expect(result.names.sort()).toEqual(['invoice-1.txt', 'invoice-2.txt'])
})

test('Synaplan reads nothing outside what the person allows', async ({ request }) => {
  test.setTimeout(90_000)
  await setReadScope(request, 'watched')

  const result = openPickup(folder)
  expect(result.sent ?? 0).toBe(0)
  expect(['refused', 'failed']).toContain(result.state)
})
