import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'

const access = {
  status: ref<{ available: boolean; connected: boolean } | null>(null),
  busy: ref(false),
  needsSignIn: ref(false),
  load: vi.fn(),
  connect: vi.fn(),
  signIn: vi.fn(),
  disconnect: vi.fn()
}

const { PopupBlockedError } = vi.hoisted(() => ({
  PopupBlockedError: class PopupBlockedError extends Error {}
}))

vi.mock('../../src/composables/useDelegation', () => ({
  PopupBlockedError,
  useDelegation: () => access
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

import DelegationSection from '../../src/components/DelegationSection.vue'

const ocButton = defineComponent({
  emits: ['click'],
  setup(_, { slots, emit, attrs }) {
    return () => h('button', { ...attrs, onClick: () => emit('click') }, slots.default?.())
  }
})

function mountCard() {
  return mount(DelegationSection, { global: { stubs: { 'oc-button': ocButton } } })
}

describe('DelegationSection', () => {
  beforeEach(() => {
    access.status.value = null
    access.load.mockReset().mockResolvedValue(undefined)
    access.connect.mockReset().mockResolvedValue(undefined)
    access.disconnect.mockReset().mockResolvedValue(undefined)
    access.signIn.mockReset().mockResolvedValue(undefined)
    access.needsSignIn.value = false
  })

  it('offers to sign in in a window when the sign-in service needs the person', async () => {
    access.status.value = { available: true, connected: false }
    access.needsSignIn.value = true
    const wrapper = mountCard()
    await flushPromises()
    expect(wrapper.find('[data-testid="synaplan-delegation-connect"]').exists()).toBe(false)
    await wrapper.get('[data-testid="synaplan-delegation-sign-in"]').trigger('click')
    expect(access.signIn).toHaveBeenCalled()
  })

  it('shows nothing when working while away is not available (shared key)', async () => {
    access.status.value = { available: false, connected: false }
    const wrapper = mountCard()
    await flushPromises()
    expect(wrapper.find('[data-testid="synaplan-delegation"]').exists()).toBe(false)
  })

  it('offers Allow when not connected and connects on click', async () => {
    access.status.value = { available: true, connected: false }
    const wrapper = mountCard()
    await flushPromises()
    expect(access.load).toHaveBeenCalled()
    await wrapper.get('[data-testid="synaplan-delegation-connect"]').trigger('click')
    expect(access.connect).toHaveBeenCalled()
  })

  it('offers Stop once connected', async () => {
    access.status.value = { available: true, connected: true }
    const wrapper = mountCard()
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-delegation-disconnect"]').trigger('click')
    expect(access.disconnect).toHaveBeenCalled()
  })

  it('says it in one sentence when connecting fails', async () => {
    access.status.value = { available: true, connected: false }
    access.connect.mockRejectedValue(new Error('boom'))
    const wrapper = mountCard()
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-delegation-connect"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-delegation-error"]').text()).not.toBe('')
  })

  it('says the sign-in window was blocked when the browser refuses it', async () => {
    access.status.value = { available: true, connected: false }
    access.needsSignIn.value = true
    access.signIn.mockRejectedValue(new PopupBlockedError('blocked'))
    const wrapper = mountCard()
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-delegation-sign-in"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-delegation-error"]').text()).toContain('pop-ups')
  })

  it('says stopping could not be confirmed when Stop fails', async () => {
    access.status.value = { available: true, connected: true }
    access.disconnect.mockRejectedValue(new Error('boom'))
    const wrapper = mountCard()
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-delegation-disconnect"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-delegation-error"]').text()).toContain('stopped')
  })
})
