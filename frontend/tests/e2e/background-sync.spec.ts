import { test, expect, APIRequestContext } from '@playwright/test'
import { getAccessToken, loginAsUser, logout } from '../../support/helpers/authHelper'
import { allowBackgroundWork, ensureRegistered } from '../../support/helpers/cloudHelper'
import { resourceId } from '../../support/helpers/davHelper'
import {
  deleteFileQuiet,
  ensureFolder,
  getPersonalSpace,
  uploadTextFile
} from '../../support/helpers/filesHelper'
import { filesInGroup, wipeKnowledgeGroup } from '../../support/helpers/synaplanApiHelper'

/**
 * Work Synaplan does from OpenCloud while the person is away, driven by the
 * reconcile pass the service otherwise runs every ten minutes:
 * - a watched folder: the service lists it over WebDAV and uploads what
 *   Synaplan asks for, as the person;
 * - a file kept up to date: after an edit the service reports the change
 *   and re-uploads it, as the person.
 * The pass is triggered here through the service's "check now" route; the
 * files are read with tokens minted from the person's offline token, not
 * with the request's token.
 */

const TEST_USER = 'testuser@synaplan.com'
const TEST_PASSWORD = 'testpass123'
const TESTS_DIR = 'synaplan-e2e'
const OC_API_URL = process.env.OC_API_URL ?? 'https://host.docker.internal:9200'
const SYNAPLAN_API_URL = process.env.SYNAPLAN_API_URL ?? 'http://localhost:8000'
const GROUP = 'OC_E2E_SYNC'

test.describe.configure({ mode: 'serial' })

let ocToken: string
let synaplanToken: string
let webDavUrl: string
const cleanup: string[] = []

test.beforeAll(async ({ browser, request }) => {
  await ensureRegistered(request)
  const { page } = await loginAsUser(browser, TEST_USER, TEST_PASSWORD)
  await allowBackgroundWork(page)
  await logout(page)
})

test.beforeEach(async ({ request }) => {
  ocToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD)
  synaplanToken = await getAccessToken(request, TEST_USER, TEST_PASSWORD, {
    clientId: 'synaplan-app',
    clientSecret: 'test-oidc-secret'
  })
  webDavUrl = (await getPersonalSpace(request, OC_API_URL, ocToken)).webDavUrl
  await ensureFolder(request, webDavUrl, ocToken, TESTS_DIR)
  await wipeKnowledgeGroup(request, SYNAPLAN_API_URL, synaplanToken, GROUP)
})

test.afterEach(async ({ request }) => {
  for (const path of cleanup.splice(0)) {
    await deleteFileQuiet(request, webDavUrl, ocToken, path)
  }
  await wipeKnowledgeGroup(request, SYNAPLAN_API_URL, synaplanToken, GROUP)
})

function service(request: APIRequestContext) {
  const headers = { Authorization: `Bearer ${ocToken}` }
  return {
    post: (path: string, data: unknown) =>
      request.post(`${OC_API_URL}/api/synaplan${path}`, { headers, data }),
    delete: (path: string) => request.delete(`${OC_API_URL}/api/synaplan${path}`, { headers }),
    checkNow: () =>
      request.post(`${OC_API_URL}/api/synaplan/watches/reconcile`, { headers, data: {} })
  }
}

test('a watched folder: the reconcile pass uploads its file as the person', async ({ request }) => {
  test.setTimeout(90_000)
  const folder = `${TESTS_DIR}/watch-sync-${Date.now()}`
  await ensureFolder(request, webDavUrl, ocToken, folder)
  cleanup.push(folder)
  await uploadTextFile(request, webDavUrl, ocToken, `${folder}/watched.txt`, 'watched content')
  const folderId = await resourceId(request, webDavUrl, ocToken, folder)

  const api = service(request)
  const started = await api.post('/watches', {
    targetKind: 'folder',
    targetId: folderId,
    preset: 'keep_searchable',
    groupKey: GROUP
  })
  expect(started.ok(), await started.text()).toBe(true)
  expect((await started.json()).uploads_enabled).toBe(true)

  try {
    expect((await api.checkNow()).ok()).toBe(true)
    const files = await filesInGroup(request, SYNAPLAN_API_URL, synaplanToken, GROUP)
    expect(files.map((f) => f.filename)).toContain('watched.txt')
    expect(files.find((f) => f.filename === 'watched.txt')?.source).toBe('opencloud')
  } finally {
    await api.delete(`/watches?targetId=${encodeURIComponent(folderId)}`)
  }
})

test('a file kept up to date: an edit reaches Synaplan in the next pass', async ({ request }) => {
  test.setTimeout(90_000)
  const path = `${TESTS_DIR}/keep-sync-${Date.now()}.txt`
  await uploadTextFile(request, webDavUrl, ocToken, path, 'first version')
  cleanup.push(path)
  const fileId = await resourceId(request, webDavUrl, ocToken, path)
  const api = service(request)
  const added = await api.post('/knowledge', {
    resourceId: fileId,
    groupKey: GROUP,
    keepSynced: true
  })
  expect(added.ok(), await added.text()).toBe(true)
  const name = path.split('/').pop()
  const kept = async () =>
    (await filesInGroup(request, SYNAPLAN_API_URL, synaplanToken, GROUP)).find(
      (f) => f.filename === name
    )
  const before = await kept()
  expect(before?.source).toBe('opencloud')
  expect(before?.text_preview).toContain('first version')

  await uploadTextFile(request, webDavUrl, ocToken, path, 'second version, longer than the first')
  expect((await api.checkNow()).ok()).toBe(true)

  const after = await kept()
  expect(after?.text_preview).toContain('second version')
  expect(after?.file_size).toBeGreaterThan(before?.file_size ?? 0)
})
