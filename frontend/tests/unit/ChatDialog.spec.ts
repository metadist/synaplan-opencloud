import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

const get = vi.fn()
const post = vi.fn()
const showErrorMessage = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { get, post } }),
  useMessages: () => ({ showErrorMessage })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s, current: 'en' })
}))

const ocStub = (tag: string) =>
  defineComponent({
    props: ['modelValue', 'options', 'disabled', 'name'],
    emits: ['click', 'update:model-value'],
    setup(props, { slots, emit }) {
      return () =>
        h(
          tag,
          {
            onClick: (e: MouseEvent) => emit('click', e),
            'data-stub': tag,
            'data-disabled': String(!!props.disabled),
            'data-options': JSON.stringify(props.options ?? null)
          },
          slots.default?.()
        )
    }
  })

import ChatDialog from '../../src/components/ChatDialog.vue'

function mountDialog() {
  return mount(ChatDialog, {
    props: { modal: { id: 'm1' } as never, resource: { id: 'res-1', name: 'invoice.txt' } },
    global: {
      stubs: {
        'oc-select': ocStub('oc-select'),
        'oc-button': ocStub('oc-button'),
        'oc-icon': ocStub('oc-icon')
      }
    }
  })
}

describe('ChatDialog', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    showErrorMessage.mockReset()
    get.mockResolvedValue({
      data: { models: [{ id: 'gpt-4o', name: 'GPT-4o', service: 'OpenAI' }] }
    })
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('shows the file and offers the models', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.text()).toContain('invoice.txt')
    const options = JSON.parse(
      wrapper.get('[data-testid="synaplan-chat-model"]').attributes('data-options') ?? 'null'
    )
    expect(options).toEqual([
      { id: '', label: 'Default model' },
      { id: 'gpt-4o', label: 'GPT-4o (OpenAI)' }
    ])
  })

  it('keeps Ask disabled until there is a question', async () => {
    const wrapper = mountDialog()
    const submit = wrapper.get('[data-testid="synaplan-chat-submit"]')
    expect(submit.attributes('data-disabled')).toBe('true')
    await wrapper.get('[data-testid="synaplan-chat-question"]').setValue('Who sent it?')
    expect(submit.attributes('data-disabled')).toBe('false')
  })

  it('asks about the file, renders the answer and saves it next to the file', async () => {
    post
      .mockResolvedValueOnce({ data: { answer: '**Nordwind** GmbH' } })
      .mockResolvedValueOnce({ data: { path: 'Invoices/Answer about invoice.md' } })
    const wrapper = mountDialog()
    const status = wrapper.get('[data-testid="synaplan-chat-status"]')
    expect(status.attributes('role')).toBe('status')
    expect(status.attributes('aria-live')).toBe('polite')

    await wrapper.get('[data-testid="synaplan-chat-question"]').setValue('Who sent it?')
    await wrapper.get('[data-testid="synaplan-chat-submit"]').trigger('click')
    await flushPromises()

    expect(post.mock.calls[0][0]).toBe('/api/synaplan/chat')
    expect(post.mock.calls[0][1]).toMatchObject({ resourceId: 'res-1', message: 'Who sent it?' })
    const result = status.get('[data-testid="synaplan-chat-result"]')
    expect(result.html()).toContain('<strong>Nordwind</strong>')
    expect(status.find('[data-testid="synaplan-chat-error"]').exists()).toBe(false)
    expect(status.find('[data-testid="synaplan-chat-loading"]').exists()).toBe(false)

    await wrapper.get('[data-testid="synaplan-chat-save"]').trigger('click')
    await flushPromises()
    expect(post.mock.calls[1][0]).toBe('/api/synaplan/files')
    expect(post.mock.calls[1][1]).toEqual({
      filename: 'Answer about invoice.md',
      text: '**Nordwind** GmbH',
      resourceId: 'res-1'
    })
    expect(wrapper.get('[data-testid="synaplan-chat-saved"]').text()).toContain(
      'Invoices/Answer about invoice.md'
    )
  })

  it('shows one sentence when asking fails', async () => {
    post.mockRejectedValueOnce({ response: { data: { code: 'no_text' } } })
    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-chat-question"]').setValue('Who sent it?')
    await wrapper.get('[data-testid="synaplan-chat-submit"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="synaplan-chat-error"]').text()).toBe(
      'Synaplan found no text in this file. It may be a scan or protected by a password.'
    )
    expect(wrapper.find('[data-testid="synaplan-chat-result"]').exists()).toBe(false)
  })

  it('cancels the question when the modal closes', async () => {
    let signal: AbortSignal | undefined
    post.mockImplementation((_u: string, _b: unknown, opts: { signal: AbortSignal }) => {
      signal = opts.signal
      return new Promise(() => {})
    })
    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-chat-question"]').setValue('Who sent it?')
    await wrapper.get('[data-testid="synaplan-chat-submit"]').trigger('click')
    expect(wrapper.find('[data-testid="synaplan-chat-loading"]').exists()).toBe(true)
    ;(wrapper.vm as unknown as { onCancel: () => void }).onCancel()
    expect(signal?.aborted).toBe(true)
  })
})
