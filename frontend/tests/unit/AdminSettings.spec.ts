import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

const get = vi.fn()
const put = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { get, put } })
}))

const interpolate = (s: string, vars?: Record<string, string>) =>
  vars ? s.replace(/%\{(\w+)\}/g, (_, key) => vars[key] ?? '') : s

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: interpolate })
}))

import AdminSettings from '../../src/components/AdminSettings.vue'

const ocButton = defineComponent({
  props: ['disabled'],
  emits: ['click'],
  setup(_, { slots, emit, attrs }) {
    return () => h('button', { ...attrs, onClick: () => emit('click') }, slots.default?.())
  }
})

const ocSelect = defineComponent({
  name: 'oc-select',
  props: ['modelValue', 'options'],
  emits: ['update:model-value'],
  setup(_, { attrs }) {
    return () => h('div', attrs)
  }
})

const ocCheckbox = defineComponent({
  name: 'oc-checkbox',
  props: ['modelValue', 'label'],
  emits: ['update:modelValue'],
  setup(props, { attrs, emit }) {
    return () =>
      h('input', {
        ...attrs,
        type: 'checkbox',
        checked: props.modelValue,
        onChange: (e: Event) => emit('update:modelValue', (e.target as HTMLInputElement).checked)
      })
  }
})

const languages = [
  { code: 'de', name: 'German' },
  { code: 'en', name: 'English' }
]

const settings = (over: Record<string, unknown> = {}) => ({
  mode: 'organisation',
  isAdmin: true,
  defaultAnswerLanguage: 'de',
  useUserLanguage: true,
  languages,
  ...over
})

function answer(
  settingsData: unknown,
  instanceData: unknown = { reachable: true, capabilities: ['deliveries', 'pickups'] }
) {
  get.mockImplementation((url: string) => {
    const data = url.endsWith('/instance') ? instanceData : settingsData
    return data instanceof Error ? Promise.reject(data) : Promise.resolve({ data })
  })
}

function mountIt() {
  return mount(AdminSettings, {
    global: {
      stubs: { 'oc-button': ocButton, 'oc-select': ocSelect, 'oc-checkbox': ocCheckbox }
    }
  })
}

describe('AdminSettings', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
  })

  it('says what Synaplan offers this OpenCloud', async () => {
    answer(settings())
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-admin-instance"]').text()).toBe(
      'Synaplan answers and offers: deliveries, pickups.'
    )
  })

  it.each([
    [{ reachable: false, capabilities: [] }, 'Synaplan cannot be reached from this OpenCloud.'],
    [{ reachable: true, capabilities: [] }, 'Synaplan answers.'],
    [new Error('502'), 'Synaplan cannot be reached from this OpenCloud.']
  ])('says when Synaplan offers nothing or cannot be reached (%#)', async (state, sentence) => {
    answer(settings(), state)
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-admin-instance"]').text()).toBe(sentence)
  })

  it.each([
    ['organisation', 'their own organisation login'],
    ['shared', 'one shared Synaplan account']
  ])('names the %s connection mode', async (mode, words) => {
    answer(settings({ mode }))
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-admin-mode"]').text()).toContain(words)
  })

  it('shows a person the answer language in force, read-only', async () => {
    answer(settings({ isAdmin: false }))
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-admin-readonly"]').text()).toBe(
      'Answers come in your OpenCloud language when Synaplan supports it, otherwise in German.'
    )
    expect(wrapper.find('[data-testid="synaplan-admin-save"]').exists()).toBe(false)
  })

  it('names only the default when people’s languages are not used', async () => {
    answer(settings({ isAdmin: false, useUserLanguage: false, defaultAnswerLanguage: 'en' }))
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-admin-readonly"]').text()).toBe(
      'Answers come in English.'
    )
  })

  it('saves the administrator’s choices', async () => {
    answer(settings())
    put.mockResolvedValueOnce({ data: { saved: true } })
    const wrapper = mountIt()
    await flushPromises()

    const select = wrapper.findComponent({ name: 'oc-select' })
    expect(select.props('modelValue')).toEqual(languages[0])
    await select.vm.$emit('update:model-value', languages[1])
    await wrapper.get('[data-testid="synaplan-admin-user-language"]').setValue(false)
    await wrapper.get('[data-testid="synaplan-admin-save"]').trigger('click')
    await flushPromises()

    expect(put).toHaveBeenCalledWith(
      '/api/synaplan/settings',
      { defaultAnswerLanguage: 'en', useUserLanguage: false },
      expect.anything()
    )
    expect(wrapper.find('[data-testid="synaplan-admin-saved"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="synaplan-admin-error"]').exists()).toBe(false)
  })

  it('says why a save failed in one sentence', async () => {
    answer(settings())
    put.mockRejectedValueOnce({ response: { data: { code: 'admin_only', error: 'x' } } })
    const wrapper = mountIt()
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-admin-save"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="synaplan-admin-error"]').text()).toBe(
      'Only an administrator can change this.'
    )
    expect(wrapper.find('[data-testid="synaplan-admin-saved"]').exists()).toBe(false)
  })

  it('says so when the settings cannot be read', async () => {
    answer(new Error('Request failed with status code 502'))
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-admin-error"]').text()).toBe(
      'Synaplan cannot be reached. Try again in a minute, or ask your administrator.'
    )
    expect(wrapper.find('[data-testid="synaplan-admin-mode"]').exists()).toBe(false)
  })
})
