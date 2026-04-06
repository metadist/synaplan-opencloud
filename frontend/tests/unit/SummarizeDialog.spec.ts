import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { computed, defineComponent, h } from 'vue'

const post = vi.fn()
const copyToClipboard = vi.fn()
const showMessage = vi.fn()
const showErrorMessage = vi.fn()
const loadingAddTask = vi.fn(async (fn: () => Promise<unknown>) => fn())

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { post } }),
  useLoadingService: () => ({ addTask: loadingAddTask }),
  useMessages: () => ({ showMessage, showErrorMessage })
}))

vi.mock('@vueuse/core', () => ({
  useClipboard: () => ({ copy: copyToClipboard })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

vi.mock('../../src/composables/useSynaplanBird', () => ({
  useSynaplanBird: () => computed(() => '/api/synaplan/assets/single_bird-dark.svg')
}))

const ocStub = (tag: string) =>
  defineComponent({
    name: tag,
    props: ['modelValue', 'options', 'disabled'],
    emits: ['click', 'update:model-value'],
    setup(_, { slots, emit }) {
      return () =>
        h(
          tag,
          { onClick: (e: MouseEvent) => emit('click', e), 'data-stub': tag },
          slots.default?.()
        )
    }
  })

const globalStubs = {
  'oc-select': ocStub('oc-select'),
  'oc-button': ocStub('oc-button'),
  'oc-icon': ocStub('oc-icon')
}

import SummarizeDialog from '../../src/components/SummarizeDialog.vue'

function mountDialog() {
  return mount(SummarizeDialog, {
    props: {
      modal: { id: 'm1' } as never,
      resource: { id: 'res-1', name: 'report.pdf', mimeType: 'application/pdf' }
    },
    global: { stubs: globalStubs }
  })
}

describe('SummarizeDialog', () => {
  beforeEach(() => {
    post.mockReset()
    copyToClipboard.mockReset()
    showMessage.mockReset()
    showErrorMessage.mockReset()
    loadingAddTask.mockClear()
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('renders the resource name and the submit button in the select phase', () => {
    const wrapper = mountDialog()
    expect(wrapper.text()).toContain('report.pdf')
    expect(wrapper.get('[data-testid="synaplan-summarize-submit"]').text()).toContain('Summarize')
  })

  it('renders the bird logo pointing at the backend assets proxy', () => {
    const wrapper = mountDialog()
    expect(wrapper.get('[data-testid="synaplan-summarize-logo"]').attributes('src')).toBe(
      '/api/synaplan/assets/single_bird-dark.svg'
    )
  })

  it('posts with the default summary type and length and renders the result', async () => {
    post.mockResolvedValueOnce({ data: { summary: 'short summary' } })

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-summarize-submit"]').trigger('click')
    await flushPromises()

    const [url, body] = post.mock.calls[0]
    expect(url).toBe('/api/synaplan/summarize')
    expect(body).toEqual({
      resourceId: 'res-1',
      summaryType: 'abstractive',
      length: 'medium'
    })

    expect(wrapper.get('[data-testid="synaplan-summarize-result"]').text()).toBe('short summary')
    expect(wrapper.find('[data-testid="synaplan-summarize-submit"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="synaplan-summarize-copy"]').exists()).toBe(true)
  })

  it('forwards type + length picker selections in the submitted body', async () => {
    post.mockResolvedValueOnce({ data: { summary: 'x' } })

    const wrapper = mountDialog()
    const selects = wrapper.findAllComponents({ name: 'oc-select' })
    expect(selects).toHaveLength(2)
    await selects[0].vm.$emit('update:model-value', { id: 'bullet-points', label: 'Bullet points' })
    await selects[1].vm.$emit('update:model-value', { id: 'long', label: 'Long' })

    await wrapper.get('[data-testid="synaplan-summarize-submit"]').trigger('click')
    await flushPromises()

    expect(post.mock.calls[0][1]).toEqual({
      resourceId: 'res-1',
      summaryType: 'bullet-points',
      length: 'long'
    })
  })

  it('surfaces a backend error in the error box', async () => {
    post.mockRejectedValueOnce(new Error('boom from backend'))

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-summarize-submit"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="synaplan-summarize-error"]').text()).toBe('boom from backend')
    expect(wrapper.find('[data-testid="synaplan-summarize-result"]').exists()).toBe(false)
  })

  it('falls back to a generic message when the thrown error has no message', async () => {
    post.mockRejectedValueOnce(new Error(''))

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-summarize-submit"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="synaplan-summarize-error"]').text()).toBe(
      'Summarization failed'
    )
  })

  it('does not paint an error state when the user cancels mid-flight', async () => {
    let rejectPending: ((e: unknown) => void) | null = null
    post.mockImplementationOnce(
      () =>
        new Promise((_, reject) => {
          rejectPending = reject
        })
    )

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-summarize-submit"]').trigger('click')
    await flushPromises()

    const exposed = wrapper.vm as unknown as { onCancel: () => void }
    exposed.onCancel()
    rejectPending!(new Error('aborted'))
    await flushPromises()

    expect(wrapper.find('[data-testid="synaplan-summarize-error"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="synaplan-summarize-result"]').exists()).toBe(false)
  })

  it('copies the result and flips the button into its copied state', async () => {
    vi.useFakeTimers()
    try {
      post.mockResolvedValueOnce({ data: { summary: 'Key points: …' } })
      copyToClipboard.mockResolvedValueOnce(undefined)

      const wrapper = mountDialog()
      await wrapper.get('[data-testid="synaplan-summarize-submit"]').trigger('click')
      await flushPromises()

      const copyBtn = wrapper.get('[data-testid="synaplan-summarize-copy"]')
      expect(copyBtn.text()).toContain('Copy')
      expect(copyBtn.text()).not.toContain('Copied')

      await copyBtn.trigger('click')
      await flushPromises()

      expect(copyToClipboard).toHaveBeenCalledWith('Key points: …')
      expect(showMessage).not.toHaveBeenCalled()
      expect(copyBtn.text()).toContain('Copied')

      vi.advanceTimersByTime(1500)
      await flushPromises()
      expect(copyBtn.text()).toContain('Copy')
      expect(copyBtn.text()).not.toContain('Copied')
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows an error toast when the clipboard write fails', async () => {
    post.mockResolvedValueOnce({ data: { summary: 'x' } })
    const clipboardError = new Error('clipboard denied')
    copyToClipboard.mockRejectedValueOnce(clipboardError)

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-summarize-submit"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-summarize-copy"]').trigger('click')
    await flushPromises()

    expect(showErrorMessage).toHaveBeenCalledWith({
      title: 'Could not copy summary',
      errors: [clipboardError]
    })
  })
})
