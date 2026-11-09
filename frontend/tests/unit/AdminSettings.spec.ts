import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

const get = vi.fn()
const put = vi.fn()
const post = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { get, put, post } })
}))

const interpolate = (s: string, vars?: Record<string, string>) =>
  vars ? s.replace(/%\{(\w+)\}/g, (_, key) => vars[key] ?? '') : s

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({
    $gettext: interpolate,
    $ngettext: (one: string, many: string, n: number, vars?: Record<string, string>) =>
      interpolate(n === 1 ? one : many, vars)
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

const instance: { registered: boolean; status: string; host: string; conflicts?: number } = {
  registered: true,
  status: 'active',
  host: 'cloud.example'
}

/** GET answers by route: the settings and the instance state are two calls. */
function answer(settingsData: typeof settings, instanceData = instance) {
  get.mockImplementation((url: string) =>
    Promise.resolve({ data: url.includes('/instance') ? instanceData : settingsData })
  )
}

describe('AdminSettings', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
    post.mockReset()
  })

  it('shows the four modes with the active one checked and the OpenCloud-only ones explained', async () => {
    answer(settings)
    const wrapper = mountIt()
    await flushPromises()
    const radios = wrapper.findAll('[data-testid="synaplan-admin-mode"] input[type="radio"]')
    expect(radios).toHaveLength(4)
    expect((radios[0].element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.text()).toContain('Not available on OpenCloud.')
    expect(wrapper.text()).toContain('One shared account')
  })

  it('lets an administrator save language and memory choices', async () => {
    answer(settings)
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
    answer({ ...settings, isAdmin: false, useUserLanguage: false })
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.find('[data-testid="synaplan-admin-language"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="synaplan-admin-readonly"]').text()).toBe(
      'Answers come in German.'
    )
  })

  it('says why saving failed in one sentence', async () => {
    answer(settings)
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

describe('AdminSettings registration', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
    post.mockReset()
  })

  it('lets an administrator register an unregistered OpenCloud with their own login', async () => {
    answer(settings, { registered: false, status: '', host: 'cloud.example' })
    post.mockResolvedValueOnce({
      data: { registered: true, status: 'active', host: 'cloud.example' }
    })
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-instance-state"]').text()).toContain(
      'Not registered yet'
    )
    await wrapper.get('[data-testid="synaplan-instance-register"]').trigger('click')
    await flushPromises()
    expect(post).toHaveBeenCalledWith('/api/synaplan/instance/register', {}, expect.anything())
    expect(wrapper.get('[data-testid="synaplan-instance-state"]').text()).toContain(
      'accepted by Synaplan'
    )
    expect(wrapper.find('[data-testid="synaplan-instance-register"]').exists()).toBe(false)
  })

  it('tells a regular user to ask their administrator and offers no button', async () => {
    answer(
      { ...settings, isAdmin: false },
      { registered: false, status: '', host: 'cloud.example' }
    )
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-instance-state"]').text()).toContain(
      'Ask your administrator'
    )
    expect(wrapper.find('[data-testid="synaplan-instance-register"]').exists()).toBe(false)
  })

  it('names a conflicting registration so the administrator can clean it up', async () => {
    answer(settings, { registered: true, status: 'active', host: 'cloud.example', conflicts: 2 })
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-instance-conflict"]').text()).toContain(
      '2 other clouds'
    )
  })

  it('says a pending instance waits for a Synaplan administrator', async () => {
    answer(settings, { registered: true, status: 'pending', host: 'cloud.example' })
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-instance-state"]').text()).toContain(
      'Waiting for a Synaplan administrator'
    )
  })
})
