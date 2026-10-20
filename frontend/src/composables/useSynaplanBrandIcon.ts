import { useConfigStore } from '@opencloud-eu/web-pkg'

// FIXME: replace the string cast with the `ImageIcon` type once the
// @opencloud-eu packages are on 9.0.0 (opencloud-eu/web#3580).
export function useSynaplanBrandIcon(pinnedTheme?: 'light' | 'dark'): string {
  const base = useConfigStore().serverUrl.replace(/\/+$/, '')
  const url = (theme: 'light' | 'dark') => `${base}/api/synaplan/assets/brand-icon?theme=${theme}`
  const icon = pinnedTheme ? { src: url(pinnedTheme) } : { src: url('light'), srcDark: url('dark') }
  return icon as unknown as string
}
