import { request } from '@playwright/test'
import { getAccessToken } from './helpers/authHelper'

/**
 * Checks that Synaplan knows this OpenCloud before any journey runs. There
 * is nothing to register: Synaplan's test stack is configured for this cloud
 * (PLATFORM_LINKS_OIDC_CLOUD_*). The Keycloak `admin` is a Synaplan
 * administrator in the dev and test realm, so the Synaplan page's status
 * route can read Synaplan's side with that person's own token.
 */
export default async function globalSetup(): Promise<void> {
  const baseUrl = process.env.BASE_URL_OC ?? 'https://host.docker.internal:9200'
  const context = await request.newContext({ ignoreHTTPSErrors: true })
  try {
    const token = await getAccessToken(context, 'admin', 'admin')
    const response = await context.get(`${baseUrl}/api/synaplan/instance`, {
      headers: { Authorization: `Bearer ${token}` }
    })
    const state = (await response.json()) as { visible?: boolean; known?: boolean; status?: string }
    if (!state.visible || !state.known || state.status !== 'active') {
      throw new Error(
        `Synaplan does not know this OpenCloud (${JSON.stringify(state)}); set PLATFORM_LINKS_OIDC_CLOUD_* on Synaplan's test stack`
      )
    }
  } finally {
    await context.dispose()
  }
}
