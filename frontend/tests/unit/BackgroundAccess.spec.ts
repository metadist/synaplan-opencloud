import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'

const access = {
  status: ref<{ available: boolean; connected: boolean } | null>(null),
  busy: ref(false),
  load: vi.fn(),
  connect: vi.fn(),
  disconnect: vi.fn(),
  resumeRedirect: vi.fn()
}

vi.mock('../../src/composables/useBackgroundAccess', () => ({
  useBackgroundAccess: () => access
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

import BackgroundAccess from '../../src/components/BackgroundAccess.vue'

const ocButton = defineComponent({
  emits: ['click'],
  setup(_, { slots, emit, attrs }) {
    return () => h('button', { ...attrs, onClick: () => emit('click') }, slots.default?.())
  }
})

function mountCard() {
  return mount(BackgroundAccess, { global: { stubs: { 'oc-button': ocButton } } })
}

describe('BackgroundAccess', () => {
  beforeEach(() => {
    access.status.value = null
    access.load.mockReset().mockResolvedValue(undefined)
    access.connect.mockReset().mockResolvedValue(undefined)
    access.disconnect.mockReset().mockResolvedValue(undefined)
    access.resumeRedirect.mockReset().mockResolvedValue(false)
  })

  it('shows nothing when background work is not available (shared key)', async () => {
    access.status.value = { available: false, connected: false }
    const wrapper = mountCard()
    await flushPromises()
    expect(wrapper.find('[data-testid="synaplan-background"]').exists()).toBe(false)
  })

  it('offers Allow when not connected and connects on click', async () => {
    access.status.value = { available: true, connected: false }
    const wrapper = mountCard()
    await flushPromises()
    expect(access.load).toHaveBeenCalled()
    await wrapper.get('[data-testid="synaplan-background-connect"]').trigger('click')
    expect(access.connect).toHaveBeenCalled()
  })

  it('offers Stop once connected', async () => {
    access.status.value = { available: true, connected: true }
    const wrapper = mountCard()
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-background-disconnect"]').trigger('click')
    expect(access.disconnect).toHaveBeenCalled()
  })

  it('says it in one sentence when connecting fails', async () => {
    access.status.value = { available: true, connected: false }
    access.connect.mockRejectedValue(new Error('boom'))
    const wrapper = mountCard()
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-background-connect"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-background-error"]').text()).not.toBe('')
  })
})
