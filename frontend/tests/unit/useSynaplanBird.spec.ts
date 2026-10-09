import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'

vi.mock('pinia', () => ({
  storeToRefs: (store: Record<string, unknown>) => store
}))

const currentTheme = ref({ isDark: false })
const serverUrl = ref('https://oc.example.com/')

vi.mock('@opencloud-eu/web-pkg', () => ({
  useThemeStore: () => ({ currentTheme }),
  useConfigStore: () => ({ serverUrl })
}))

import { useSynaplanBird } from '../../src/composables/useSynaplanBird'

describe('useSynaplanBird', () => {
  beforeEach(() => {
    currentTheme.value = { isDark: false }
    serverUrl.value = 'https://oc.example.com/'
  })

  it('asks for the light theme icon on a light theme', () => {
    expect(useSynaplanBird().value).toBe(
      'https://oc.example.com/api/synaplan/assets/brand-icon?theme=light'
    )
  })

  it('asks for the dark theme icon on a dark theme', () => {
    currentTheme.value = { isDark: true }
    expect(useSynaplanBird().value).toBe(
      'https://oc.example.com/api/synaplan/assets/brand-icon?theme=dark'
    )
  })

  it('reacts to theme changes', () => {
    const src = useSynaplanBird()
    expect(src.value).toBe('https://oc.example.com/api/synaplan/assets/brand-icon?theme=light')
    currentTheme.value = { isDark: true }
    expect(src.value).toBe('https://oc.example.com/api/synaplan/assets/brand-icon?theme=dark')
  })

  it('reacts to serverUrl changes', () => {
    const src = useSynaplanBird()
    expect(src.value).toBe('https://oc.example.com/api/synaplan/assets/brand-icon?theme=light')
    serverUrl.value = 'https://other.example.org/'
    expect(src.value).toBe('https://other.example.org/api/synaplan/assets/brand-icon?theme=light')
  })

  it('strips trailing slashes from the server URL so the path stays single-slash', () => {
    serverUrl.value = 'https://oc.example.com///'
    expect(useSynaplanBird().value).toBe(
      'https://oc.example.com/api/synaplan/assets/brand-icon?theme=light'
    )
  })

  it('works when serverUrl has no trailing slash', () => {
    serverUrl.value = 'https://oc.example.com'
    expect(useSynaplanBird().value).toBe(
      'https://oc.example.com/api/synaplan/assets/brand-icon?theme=light'
    )
  })
})
