import { computed, type ComputedRef } from 'vue'
import { storeToRefs } from 'pinia'
import { useConfigStore, useThemeStore } from '@opencloud-eu/web-pkg'

/**
 * URL of the Synaplan brand-mark SVG served by the backend's
 * /api/synaplan/assets proxy, pinned to configStore.serverUrl so it
 * resolves against OC Web regardless of which port the SPA is
 * served from, and swapped light/dark to contrast the active theme.
 */
export function useSynaplanBird(): ComputedRef<string> {
  const themeStore = useThemeStore()
  const configStore = useConfigStore()
  const { currentTheme } = storeToRefs(themeStore)
  const { serverUrl } = storeToRefs(configStore)

  return computed(() => {
    const variant = currentTheme.value.isDark ? 'light' : 'dark'
    const base = serverUrl.value.replace(/\/+$/, '')
    return `${base}/api/synaplan/assets/single_bird-${variant}.svg`
  })
}
