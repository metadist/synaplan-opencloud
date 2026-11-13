import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

const settings = { login: vi.fn(), load: vi.fn(), save: vi.fn() }

vi.mock('../../src/composables/useReadScope', () => ({
  useReadScope: () => settings
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

import AccountCard from '../../src/components/AccountCard.vue'

const ocButton = defineComponent({
  emits: ['click'],
  setup(_, { slots, emit, attrs }) {
    return () => h('button', { ...attrs, onClick: () => emit('click') }, slots.default?.())
  }
})

async function mountCard() {
  const wrapper = mount(AccountCard, { global: { stubs: { 'oc-button': ocButton } } })
  await flushPromises()
  return wrapper
}

const allowed = {
  mode: 'allowed',
  folders: [
    { id: 'f1', path: 'Specs' },
    { id: 'f2', path: 'Invoices/2026' }
  ]
}

describe('AccountCard', () => {
  beforeEach(() => {
    settings.login.mockReset().mockResolvedValue('person')
    settings.load.mockReset().mockResolvedValue({ available: true, readScope: allowed })
    settings.save.mockReset().mockImplementation((scope) => Promise.resolve(scope))
  })

  it('says the OpenCloud login is the Synaplan login and shows the setting', async () => {
    const wrapper = await mountCard()
    expect(wrapper.get('[data-testid="synaplan-settings-login"]').text()).toBe(
      'Your OpenCloud login is your Synaplan login.'
    )
    const allowedRadio = wrapper.get('[data-testid="synaplan-settings-read-allowed"]')
    expect((allowedRadio.element as HTMLInputElement).checked).toBe(true)
    expect(
      wrapper.findAll('[data-testid="synaplan-settings-folder"]').map((li) => li.text())
    ).toEqual(['Specs Remove', 'Invoices/2026 Remove'])
  })

  it('shows no setting with the shared key', async () => {
    settings.login.mockResolvedValue('shared-key')
    settings.load.mockResolvedValue({ available: false })
    const wrapper = await mountCard()
    expect(wrapper.get('[data-testid="synaplan-settings-login"]').text()).toBe(
      'This OpenCloud uses one shared Synaplan account for everyone.'
    )
    expect(wrapper.find('[data-testid="synaplan-settings-save"]').exists()).toBe(false)
  })

  it('saves another mode', async () => {
    const wrapper = await mountCard()
    await wrapper.get('[data-testid="synaplan-settings-read-all"]').setValue(true)
    await wrapper.get('[data-testid="synaplan-settings-save"]').trigger('click')
    await flushPromises()
    expect(settings.save).toHaveBeenCalledWith({ mode: 'all', folders: allowed.folders })
    expect(wrapper.find('[data-testid="synaplan-settings-saved"]').exists()).toBe(true)
  })

  it('removes a folder and saves the rest', async () => {
    const wrapper = await mountCard()
    await wrapper.findAll('[data-testid="synaplan-settings-folder-remove"]')[0].trigger('click')
    await wrapper.get('[data-testid="synaplan-settings-save"]').trigger('click')
    await flushPromises()
    expect(settings.save).toHaveBeenCalledWith({
      mode: 'allowed',
      folders: [{ id: 'f2', path: 'Invoices/2026' }]
    })
  })

  it('shows the folders the service kept', async () => {
    settings.save.mockResolvedValue({ mode: 'allowed', folders: [{ id: 'f2', path: 'Moved' }] })
    const wrapper = await mountCard()
    await wrapper.get('[data-testid="synaplan-settings-save"]').trigger('click')
    await flushPromises()
    expect(
      wrapper.findAll('[data-testid="synaplan-settings-folder"]').map((li) => li.text())
    ).toEqual(['Moved Remove'])
  })

  it('shows one sentence when saving fails', async () => {
    settings.save.mockRejectedValue({ response: { data: { code: 'cloud_ambiguous' } } })
    const wrapper = await mountCard()
    await wrapper.get('[data-testid="synaplan-settings-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-settings-error"]').text()).toBe(
      'Synaplan knows this OpenCloud more than once. Ask your administrator.'
    )
    expect(wrapper.find('[data-testid="synaplan-settings-saved"]').exists()).toBe(false)
  })

  it('shows one sentence when the setting cannot be read', async () => {
    settings.load.mockRejectedValue(new Error('Request failed with status code 502'))
    const wrapper = await mountCard()
    expect(wrapper.get('[data-testid="synaplan-settings-error"]').text()).toBe(
      'Synaplan cannot be reached. Try again in a minute, or ask your administrator.'
    )
  })
})
