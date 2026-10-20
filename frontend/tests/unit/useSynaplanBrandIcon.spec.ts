import { describe, it, expect, vi } from 'vitest'

const config = { serverUrl: 'https://oc.example.com/' }

vi.mock('@opencloud-eu/web-pkg', () => ({
  useConfigStore: () => config
}))

import { useSynaplanBrandIcon } from '../../src/composables/useSynaplanBrandIcon'

describe('useSynaplanBrandIcon', () => {
  it('points both theme variants at the brand icon endpoint', () => {
    expect(useSynaplanBrandIcon()).toEqual({
      src: 'https://oc.example.com/api/synaplan/assets/brand-icon?theme=light',
      srcDark: 'https://oc.example.com/api/synaplan/assets/brand-icon?theme=dark'
    })
  })

  it('can pin one theme variant, for use on a coloured background', () => {
    expect(useSynaplanBrandIcon('dark')).toEqual({
      src: 'https://oc.example.com/api/synaplan/assets/brand-icon?theme=dark'
    })
  })

  it.each(['https://oc.example.com', 'https://oc.example.com///'])(
    'keeps the path single-slash for server URL %s',
    (serverUrl) => {
      config.serverUrl = serverUrl
      expect(useSynaplanBrandIcon()).toMatchObject({
        src: 'https://oc.example.com/api/synaplan/assets/brand-icon?theme=light'
      })
    }
  )
})
