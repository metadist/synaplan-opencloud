import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

const get = vi.fn()
const put = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { get, put } })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({
    $gettext: (s: string, vars?: Record<string, string>) =>
      vars ? s.replace(/%\{(\w+)\}/g, (_, key) => vars[key] ?? '') : s
  })
}))

import AdminSettings from '../../src/components/AdminSettings.vue'

const stub = (tag: string) =>
  defineComponent({
    props: ['modelValue', 'options', 'disabled', 'label'],
    emits: ['click', 'update:modelValue'],
    setup(props, { slots, emit }) {
      return () =>
        h(
          tag,
          {
            'data-stub': tag,
            onClick: () => emit('click'),
            onChange: (e: Event) =>
              emit('update:modelValue', (e.target as HTMLInputElement).checked)
          },
          slots.default?.()
        )
    }
  })

const settings = {
  mode: 'organisation',
  isAdmin: true,
  defaultAnswerLanguage: 'de',
  useUserLanguage: true,
  allowMemories: false,
  memoriesAvailable: true,
  languages: [
    { code: 'de', name: 'German' },
    { code: 'en', name: 'English' }
  ]
}

function mountIt() {
  return mount(AdminSettings, {
    global: {
      stubs: {
        'oc-button': stub('oc-button'),
        'oc-select': stub('oc-select'),
        'oc-checkbox': stub('oc-checkbox')
      }
    }
  })
}

describe('AdminSettings', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
  })

  it('shows the four modes with the active one checked and the OpenCloud-only ones explained', async () => {
    get.mockResolvedValueOnce({ data: settings })
    const wrapper = mountIt()
    await flushPromises()
    const radios = wrapper.findAll('[data-testid="synaplan-admin-mode"] input[type="radio"]')
    expect(radios).toHaveLength(4)
    expect((radios[0].element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.text()).toContain('Not available on OpenCloud.')
    expect(wrapper.text()).toContain('One shared account')
  })

  it('lets an administrator save language and memory choices', async () => {
    get.mockResolvedValueOnce({ data: settings })
    put.mockResolvedValueOnce({ data: { saved: true } })
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.find('[data-testid="synaplan-admin-language"]').exists()).toBe(true)
    await wrapper.get('[data-testid="synaplan-admin-save"]').trigger('click')
    await flushPromises()
    expect(put).toHaveBeenCalledWith(
      '/api/synaplan/settings',
      { defaultAnswerLanguage: 'de', useUserLanguage: true, allowMemories: false },
      expect.anything()
    )
    expect(wrapper.find('[data-testid="synaplan-admin-saved"]').exists()).toBe(true)
  })

  it('shows a person who is not an administrator only the answer-language sentence', async () => {
    get.mockResolvedValueOnce({ data: { ...settings, isAdmin: false, useUserLanguage: false } })
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.find('[data-testid="synaplan-admin-language"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="synaplan-admin-readonly"]').text()).toBe(
      'Answers come in German.'
    )
  })

  it('says why saving failed in one sentence', async () => {
    get.mockResolvedValueOnce({ data: settings })
    put.mockRejectedValueOnce({
      response: { data: { code: 'admin_only', error: 'Only an administrator can change this.' } }
    })
    const wrapper = mountIt()
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-admin-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-admin-error"]').text()).toBe(
      'Only an administrator can change this.'
    )
  })
})
