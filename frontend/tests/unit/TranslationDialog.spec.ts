import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

// --- Composable mocks -----------------------------------------------
//
// TranslationDialog pulls useClientService, useLoadingService and
// useMessages from web-pkg and useClipboard from @vueuse/core. None
// of those should talk to a real backend during a unit test — so stub
// the whole modules and expose the mock fns at module scope.

const post = vi.fn()
const copyToClipboard = vi.fn()
const showMessage = vi.fn()
const showErrorMessage = vi.fn()

// loadingService.addTask is "invoke this thunk, return its promise" in
// the production implementation. We mirror that so the dialog's real
// translate() flow still runs — we're only suppressing the global
// loading-indicator side effect.
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

// The dialog uses a bunch of global oc-* design-system components. We
// don't care what they render — stub them as passthrough divs/buttons
// that forward their listeners and default slot so the component's
// logic (click → translate, update:model-value → select) still works.
const ocStub = (tag: string) =>
  defineComponent({
    props: ['modelValue', 'options', 'disabled'],
    emits: ['click', 'update:model-value'],
    setup(_, { slots, emit }) {
      return () =>
        h(
          tag,
          {
            onClick: (e: MouseEvent) => emit('click', e),
            'data-stub': tag
          },
          slots.default?.()
        )
    }
  })

const globalStubs = {
  'oc-select': ocStub('oc-select'),
  'oc-button': ocStub('oc-button'),
  'oc-icon': ocStub('oc-icon')
}

import TranslationDialog from '../../src/components/TranslationDialog.vue'

function mountDialog() {
  return mount(TranslationDialog, {
    props: {
      // Minimal Modal shape — the dialog only needs the modal prop for
      // defineProps type-checking; it never reaches into it directly.
      modal: { id: 'm1' } as never,
      resource: { id: 'res-1', name: 'report.pdf', mimeType: 'application/pdf' }
    },
    global: { stubs: globalStubs }
  })
}

describe('TranslationDialog', () => {
  beforeEach(() => {
    post.mockReset()
    copyToClipboard.mockReset()
    showMessage.mockReset()
    showErrorMessage.mockReset()
    loadingAddTask.mockClear()
    // Error-path tests exercise the dialog's own console.error logging
    // on purpose. Silence it so the vitest output isn't littered with
    // the expected noise — any unexpected calls still fail assertions.
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('renders the resource name in the select phase', () => {
    const wrapper = mountDialog()
    expect(wrapper.text()).toContain('report.pdf')
    expect(wrapper.get('[data-testid="synaplan-translation-submit"]').text()).toContain('Translate')
  })

  it('posts the translate request with resourceId + targetLanguage and renders the result', async () => {
    post.mockResolvedValueOnce({ data: { translation: 'Hallo Welt' } })

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-translation-submit"]').trigger('click')
    await flushPromises()

    expect(post).toHaveBeenCalledTimes(1)
    const [url, body] = post.mock.calls[0]
    expect(url).toBe('/api/synaplan/translate')
    expect(body).toEqual({ resourceId: 'res-1', targetLanguage: 'en' })

    const result = wrapper.get('[data-testid="synaplan-translation-result"]')
    expect(result.text()).toBe('Hallo Welt')
    // Submit button is replaced by Copy button once a translation is shown.
    expect(wrapper.find('[data-testid="synaplan-translation-submit"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="synaplan-translation-copy"]').exists()).toBe(true)
  })

  it('surfaces a backend error in the error box', async () => {
    post.mockRejectedValueOnce(new Error('boom from backend'))

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-translation-submit"]').trigger('click')
    await flushPromises()

    const err = wrapper.get('[data-testid="synaplan-translation-error"]')
    expect(err.text()).toBe('boom from backend')
    // Still in the select/error phase — the result block is not rendered.
    expect(wrapper.find('[data-testid="synaplan-translation-result"]').exists()).toBe(false)
  })

  it('falls back to a generic message when the thrown error has no message', async () => {
    post.mockRejectedValueOnce(new Error(''))

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-translation-submit"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="synaplan-translation-error"]').text()).toBe(
      'Translation failed'
    )
  })

  it('does not paint an error state when the user cancels mid-flight', async () => {
    // Simulate an aborted request: the dialog's onCancel() aborts the
    // in-flight AbortController, and the http client rejects with an
    // error once signal.aborted is true. We emulate that by rejecting
    // AFTER the caller has had a chance to invoke onCancel().
    let rejectPending: ((e: unknown) => void) | null = null
    post.mockImplementationOnce(
      () =>
        new Promise((_, reject) => {
          rejectPending = reject
        })
    )

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-translation-submit"]').trigger('click')
    await flushPromises()

    // Component-under-test exposes onCancel via defineExpose — the
    // modal host calls it; we invoke it directly here.
    const exposed = wrapper.vm as unknown as { onCancel: () => void }
    exposed.onCancel()
    rejectPending!(new Error('aborted'))
    await flushPromises()

    expect(wrapper.find('[data-testid="synaplan-translation-error"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="synaplan-translation-result"]').exists()).toBe(false)
  })

  it('copies the result to the clipboard and shows a confirmation message', async () => {
    post.mockResolvedValueOnce({ data: { translation: 'Bonjour le monde' } })
    copyToClipboard.mockResolvedValueOnce(undefined)

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-translation-submit"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-translation-copy"]').trigger('click')
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith('Bonjour le monde')
    expect(showMessage).toHaveBeenCalledWith({
      title: 'Translation copied to your clipboard.'
    })
  })

  it('shows an error toast when the clipboard write fails', async () => {
    post.mockResolvedValueOnce({ data: { translation: 'x' } })
    const clipboardError = new Error('clipboard denied')
    copyToClipboard.mockRejectedValueOnce(clipboardError)

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-translation-submit"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-translation-copy"]').trigger('click')
    await flushPromises()

    expect(showErrorMessage).toHaveBeenCalledWith({
      title: 'Could not copy translation',
      errors: [clipboardError]
    })
  })
})
