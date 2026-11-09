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

const instance = {
  visible: true,
  known: true,
  status: 'active',
  host: 'cloud.example',
  deliveryUrl: 'http://svc/inbound',
  reachable: true as boolean | null
}

/** GET answers by route: the settings and the instance state are two calls. */
function answer(settingsData: typeof settings, instanceData: Record<string, unknown> = instance) {
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

describe('AdminSettings, this OpenCloud in Synaplan', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
    post.mockReset()
  })

  it('an admin of both sees the host and delivery address Synaplan has configured', async () => {
    answer(settings, {
      visible: true,
      known: true,
      status: 'active',
      host: 'cloud.example',
      deliveryUrl: 'http://synaplan-opencloud:9106/api/synaplan/inbound',
      reachable: true
    })
    const wrapper = mountIt()
    await flushPromises()
    const state = wrapper.get('[data-testid="synaplan-instance-state"]').text()
    expect(state).toContain('cloud.example')
    expect(state).toContain('reaches it at http://synaplan-opencloud:9106/api/synaplan/inbound')
    expect(post).not.toHaveBeenCalled()
  })

  it('an admin of both is told when Synaplan cannot reach the delivery address', async () => {
    answer(settings, {
      visible: true,
      known: true,
      status: 'active',
      host: 'cloud.example',
      deliveryUrl: 'http://nowhere/inbound',
      reachable: false
    })
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-instance-state"]').text()).toContain(
      'cannot reach it'
    )
  })

  it('names the Synaplan setting when Synaplan has no configuration for this OpenCloud', async () => {
    answer(settings, { visible: true, known: false })
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-instance-state"]').text()).toContain(
      'PLATFORM_LINKS_OIDC_CLOUD'
    )
  })

  it('someone who is not a Synaplan admin only learns that Synaplan configures it', async () => {
    answer({ ...settings, isAdmin: false }, { visible: false, known: false })
    const wrapper = mountIt()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-instance-state"]').text()).toContain(
      'from its own configuration'
    )
    expect(wrapper.find('[data-testid="synaplan-instance-register"]').exists()).toBe(false)
  })
})
