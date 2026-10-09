import { computed, type ComputedRef } from 'vue'
import { storeToRefs } from 'pinia'
import { useConfigStore, useThemeStore } from '@opencloud-eu/web-pkg'

/**
 * URL of the Synaplan brand icon served by the backend's
 * /api/synaplan/assets/brand-icon endpoint: the operator's
 * white-label icon when Synaplan has one configured, the bird
 * otherwise. Pinned to configStore.serverUrl so it resolves against
 * OC Web regardless of which port the SPA is served from. The theme
 * lets the backend pick a bird that contrasts with it.
 */
export function useSynaplanBird(): ComputedRef<string> {
  const themeStore = useThemeStore()
  const configStore = useConfigStore()
  const { currentTheme } = storeToRefs(themeStore)
  const { serverUrl } = storeToRefs(configStore)

  return computed(() => {
    const theme = currentTheme.value.isDark ? 'dark' : 'light'
    const base = serverUrl.value.replace(/\/+$/, '')
    return `${base}/api/synaplan/assets/brand-icon?theme=${theme}`
  })
}
