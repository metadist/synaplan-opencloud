import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { computed, defineComponent, h } from 'vue'

const get = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { get } })
}))

vi.mock('../../src/composables/useSynaplanBird', () => ({
  useSynaplanBird: () =>
    computed(() => 'https://oc.example.com/api/synaplan/assets/single_bird-dark.svg')
}))

const ocStub = (tag: string) =>
  defineComponent({
    props: ['disabled'],
    setup(_, { slots }) {
      return () => h(tag, { 'data-stub': tag }, slots.default?.())
    }
  })

const globalStubs = {
  'oc-button': ocStub('oc-button')
}

import Synaplan from '../../src/views/Synaplan.vue'

function mountView(props: { synaplanUrl?: string } = {}) {
  return mount(Synaplan, {
    props,
    global: { stubs: globalStubs }
  })
}

describe('Synaplan view', () => {
  beforeEach(() => {
    get.mockReset()
  })

  it('renders a bare heading when no synaplanUrl is configured', () => {
    const wrapper = mountView()
    const title = wrapper.get('[data-testid="synaplan-title"]')
    expect(title.text()).toContain('Synaplan')
    expect(wrapper.find('[data-testid="synaplan-title-link"]').exists()).toBe(false)
  })

  it('renders a link to the synaplan install when synaplanUrl is set', () => {
    const wrapper = mountView({ synaplanUrl: 'https://synaplan.example.com' })
    const link = wrapper.get('[data-testid="synaplan-title-link"]')
    expect(link.attributes('href')).toBe('https://synaplan.example.com')
    expect(link.attributes('target')).toBe('_blank')
    expect(link.attributes('rel')).toBe('noopener noreferrer')
  })

  it('renders the Synaplan bird logo pointing at the backend assets proxy', () => {
    const wrapper = mountView({ synaplanUrl: 'https://synaplan.example.com' })
    const logo = wrapper.get('[data-testid="synaplan-title-logo"]')
    expect(logo.attributes('src')).toBe(
      'https://oc.example.com/api/synaplan/assets/single_bird-dark.svg'
    )
  })

  it('links to the Synaplan AI models config page when synaplanUrl is set', () => {
    const wrapper = mountView({ synaplanUrl: 'https://synaplan.example.com' })
    const link = wrapper.get('[data-testid="synaplan-models-link"]')
    expect(link.attributes('href')).toBe('https://synaplan.example.com/config/ai-models')
    expect(link.attributes('target')).toBe('_blank')
  })

  it('strips trailing slashes from synaplanUrl when building the models link', () => {
    const wrapper = mountView({ synaplanUrl: 'https://synaplan.example.com///' })
    expect(wrapper.get('[data-testid="synaplan-models-link"]').attributes('href')).toBe(
      'https://synaplan.example.com/config/ai-models'
    )
  })

  it('hides the models config link when no synaplanUrl is configured', () => {
    const wrapper = mountView()
    expect(wrapper.find('[data-testid="synaplan-models-link"]').exists()).toBe(false)
  })
})
