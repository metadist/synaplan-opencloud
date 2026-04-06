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

  it('returns the dark-fill variant on a light theme', () => {
    expect(useSynaplanBird().value).toBe(
      'https://oc.example.com/api/synaplan/assets/single_bird-dark.svg'
    )
  })

  it('returns the light-fill variant on a dark theme', () => {
    currentTheme.value = { isDark: true }
    expect(useSynaplanBird().value).toBe(
      'https://oc.example.com/api/synaplan/assets/single_bird-light.svg'
    )
  })

  it('reacts to theme changes', () => {
    const src = useSynaplanBird()
    expect(src.value).toBe('https://oc.example.com/api/synaplan/assets/single_bird-dark.svg')
    currentTheme.value = { isDark: true }
    expect(src.value).toBe('https://oc.example.com/api/synaplan/assets/single_bird-light.svg')
  })

  it('reacts to serverUrl changes', () => {
    const src = useSynaplanBird()
    expect(src.value).toBe('https://oc.example.com/api/synaplan/assets/single_bird-dark.svg')
    serverUrl.value = 'https://other.example.org/'
    expect(src.value).toBe('https://other.example.org/api/synaplan/assets/single_bird-dark.svg')
  })

  it('strips trailing slashes from the server URL so the path stays single-slash', () => {
    serverUrl.value = 'https://oc.example.com///'
    expect(useSynaplanBird().value).toBe(
      'https://oc.example.com/api/synaplan/assets/single_bird-dark.svg'
    )
  })

  it('works when serverUrl has no trailing slash', () => {
    serverUrl.value = 'https://oc.example.com'
    expect(useSynaplanBird().value).toBe(
      'https://oc.example.com/api/synaplan/assets/single_bird-dark.svg'
    )
  })
})
