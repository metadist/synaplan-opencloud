import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

const get = vi.fn()
const put = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { get, put } })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

const ocButton = defineComponent({
  name: 'oc-button',
  props: ['disabled'],
  emits: ['click'],
  setup(props, { slots, emit }) {
    return () =>
      h(
        'oc-button',
        {
          onClick: (e: MouseEvent) => {
            if (!props.disabled) emit('click', e)
          },
          disabled: props.disabled
        },
        slots.default?.()
      )
  }
})

import ReadFolderDialog from '../../src/components/ReadFolderDialog.vue'

const scope = (overrides: Record<string, unknown> = {}) => ({
  data: { mode: 'watched', named: false, allowed: false, readsWhileAway: true, ...overrides }
})

function mountDialog() {
  return mount(ReadFolderDialog, {
    props: { modal: { id: 'm1' } as never, resource: { id: 's$sp!hr', name: 'HR' } },
    global: { stubs: { 'oc-button': ocButton } }
  })
}

const testid = (id: string) => `[data-testid="synaplan-read-${id}"]`

describe('ReadFolderDialog', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
    get.mockResolvedValue(scope())
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('asks about this folder', async () => {
    mountDialog()
    await flushPromises()
    expect(get).toHaveBeenCalledWith(
      '/api/synaplan/read-scope?folderId=s%24sp!hr',
      expect.anything()
    )
  })

  it('allows reading a folder that is not allowed yet', async () => {
    put.mockResolvedValueOnce(scope({ mode: 'allowed', named: true, allowed: true }))
    const wrapper = mountDialog()
    await flushPromises()

    expect(wrapper.find(testid('not-allowed')).exists()).toBe(true)
    expect(wrapper.find(testid('stop')).exists()).toBe(false)
    await wrapper.get(testid('allow')).trigger('click')
    await flushPromises()

    expect(put).toHaveBeenCalledWith(
      '/api/synaplan/read-scope',
      { folderId: 's$sp!hr', allow: true },
      expect.anything()
    )
    expect(wrapper.find(testid('allowed')).exists()).toBe(true)
    expect(wrapper.find(testid('allow')).exists()).toBe(false)
  })

  it('stops reading a folder the person allowed', async () => {
    get.mockResolvedValueOnce(scope({ mode: 'allowed', named: true, allowed: true }))
    put.mockResolvedValueOnce(scope())
    const wrapper = mountDialog()
    await flushPromises()

    await wrapper.get(testid('stop')).trigger('click')
    await flushPromises()

    expect(put).toHaveBeenCalledWith(
      '/api/synaplan/read-scope',
      { folderId: 's$sp!hr', allow: false },
      expect.anything()
    )
    expect(wrapper.find(testid('not-allowed')).exists()).toBe(true)
  })

  it.each([
    ['allowed', 'Synaplan may read this folder because you allowed a folder it is in.'],
    ['all', 'Synaplan may read all your folders, this one included.']
  ])('explains a folder allowed by %s and offers nothing to change', async (mode, sentence) => {
    get.mockResolvedValueOnce(scope({ mode, allowed: true }))
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.get(testid('inherited')).text()).toBe(sentence)
    expect(wrapper.find(testid('allow')).exists()).toBe(false)
    expect(wrapper.find(testid('stop')).exists()).toBe(false)
  })

  it('says when reads cannot run while the person is away', async () => {
    get.mockResolvedValueOnce(scope({ readsWhileAway: false }))
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.find(testid('not-away')).exists()).toBe(true)
  })

  it('shows one readable sentence when the setting cannot be loaded', async () => {
    get.mockReset()
    get.mockRejectedValueOnce({ response: { data: { code: 'reads_unavailable' } } })
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.get(testid('error')).text()).toBe(
      'Synaplan cannot read folders in this OpenCloud. Ask your administrator.'
    )
    expect(wrapper.find(testid('allow')).exists()).toBe(false)
    expect(wrapper.find(testid('loading')).exists()).toBe(false)
  })

  it('shows the fallback when a change fails without a code', async () => {
    put.mockRejectedValueOnce(new Error('Request failed with status code 502'))
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get(testid('allow')).trigger('click')
    await flushPromises()
    expect(wrapper.get(testid('error')).text()).toBe(
      'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
    )
    expect(wrapper.find(testid('not-allowed')).exists()).toBe(true)
  })
})
