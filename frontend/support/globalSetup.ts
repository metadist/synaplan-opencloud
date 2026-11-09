import { request } from '@playwright/test'
import { getAccessToken } from './helpers/authHelper'

/**
 * Checks that Synaplan answers this OpenCloud and takes its people before
 * any journey runs. There is nothing to register: Synaplan's test stack is
 * configured for this cloud (PLATFORM_LINKS_OIDC_CLOUD_*), and the person is
 * linked on their first call through it.
 */
export default async function globalSetup(): Promise<void> {
  const baseUrl = process.env.BASE_URL_OC ?? 'https://host.docker.internal:9200'
  const context = await request.newContext({ ignoreHTTPSErrors: true })
  try {
    const token = await getAccessToken(context, 'admin', 'admin')
    const headers = { Authorization: `Bearer ${token}` }
    const instance = (await (
      await context.get(`${baseUrl}/api/synaplan/instance`, { headers })
    ).json()) as {
      reachable?: boolean
    }
    const me = await context.get(`${baseUrl}/api/synaplan/me`, { headers })
    if (!instance.reachable || !me.ok()) {
      throw new Error(
        `Synaplan does not take people from this OpenCloud (instance ${JSON.stringify(instance)}, me ${me.status()}); set PLATFORM_LINKS_OIDC_CLOUD_* on Synaplan's test stack`
      )
    }
  } finally {
    await context.dispose()
  }
}
