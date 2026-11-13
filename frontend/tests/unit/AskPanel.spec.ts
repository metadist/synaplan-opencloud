import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

const get = vi.fn()
const post = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { get, post } })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s, current: 'fr' })
}))

const ocStub = (tag: string) =>
  defineComponent({
    props: ['modelValue', 'options', 'disabled'],
    emits: ['click', 'update:model-value'],
    setup(props, { slots, emit }) {
      return () =>
        h(
          tag,
          {
            onClick: (e: MouseEvent) => emit('click', e),
            'data-stub': tag,
            'data-disabled': String(!!props.disabled)
          },
          slots.default?.()
        )
    }
  })

import AskPanel from '../../src/components/AskPanel.vue'

function mountPanel() {
  return mount(AskPanel, {
    global: { stubs: { 'oc-select': ocStub('oc-select'), 'oc-button': ocStub('oc-button') } }
  })
}

describe('AskPanel', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    get.mockResolvedValue({ data: { models: [] } })
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('asks in general and renders the answer', async () => {
    post.mockResolvedValueOnce({ data: { answer: 'Bonjour' } })
    const wrapper = mountPanel()
    const submit = wrapper.get('[data-testid="synaplan-ask-submit"]')
    expect(submit.attributes('data-disabled')).toBe('true')
    await wrapper.get('[data-testid="synaplan-ask-question"]').setValue('Hello')
    await submit.trigger('click')
    await flushPromises()

    expect(post.mock.calls[0][1]).toEqual({ message: 'Hello', model: '', language: 'fr' })
    expect(wrapper.get('[data-testid="synaplan-ask-result"]').text()).toBe('Bonjour')
    expect(wrapper.find('[data-testid="synaplan-ask-error"]').exists()).toBe(false)
  })

  it('shows one sentence when asking fails', async () => {
    post.mockRejectedValueOnce(new Error('Request failed with status code 502'))
    const wrapper = mountPanel()
    await wrapper.get('[data-testid="synaplan-ask-question"]').setValue('Hello')
    await wrapper.get('[data-testid="synaplan-ask-submit"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="synaplan-ask-error"]').text()).toBe(
      'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
    )
  })
})
