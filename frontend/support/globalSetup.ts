import { request } from '@playwright/test'
import { getAccessToken } from './helpers/authHelper'

/**
 * Connects this OpenCloud to Synaplan the way an administrator does:
 * the Keycloak `admin` opens the Synaplan page and registers the
 * instance. Their own identity travels through the standard token
 * exchange; because the dev and test realm make that person a Synaplan
 * administrator too, the instance is active at once. Nothing is minted
 * here — no API key, no credential in a log or an environment — and the
 * step is idempotent, so the suite runs again on the same stacks.
 */
export default async function globalSetup(): Promise<void> {
  const baseUrl = process.env.BASE_URL_OC ?? 'https://host.docker.internal:9200'
  const context = await request.newContext({ ignoreHTTPSErrors: true })
  try {
    const token = await getAccessToken(context, 'admin', 'admin')
    const response = await context.post(`${baseUrl}/api/synaplan/instance/register`, {
      headers: { Authorization: `Bearer ${token}` }
    })
    if (!response.ok()) {
      throw new Error(
        `registering this OpenCloud with Synaplan failed: ${response.status()} ${await response.text()}`
      )
    }
    const state = (await response.json()) as { status?: string }
    if (state.status !== 'active') {
      throw new Error(
        `this OpenCloud is ${state.status ?? 'unregistered'} in Synaplan; the Keycloak admin must be a Synaplan administrator`
      )
    }
  } finally {
    await context.dispose()
  }
}
