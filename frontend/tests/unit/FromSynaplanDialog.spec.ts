import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

const get = vi.fn()
const post = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { get, post } })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({
    $gettext: (s: string, params?: Record<string, string>) =>
      s.replace(/%\{(\w+)\}/g, (_, key: string) => params?.[key] ?? '')
  })
}))

import FromSynaplanDialog from '../../src/components/FromSynaplanDialog.vue'

const ocButton = defineComponent({
  name: 'oc-button',
  props: ['disabled'],
  emits: ['click'],
  setup(props, { slots, emit, attrs }) {
    return () =>
      h(
        'button',
        { ...attrs, disabled: props.disabled, onClick: () => emit('click') },
        slots.default?.()
      )
  }
})

function page(files: object[], pages = 1) {
  return { data: { files, pagination: { page: 1, pages } } }
}

function mountDialog() {
  return mount(FromSynaplanDialog, {
    props: { modal: { id: 'm1' } as never, resource: { id: 's!folder', name: 'Reports' } },
    global: { stubs: { 'oc-button': ocButton, 'oc-text-input': true } }
  })
}

describe('FromSynaplanDialog', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
  })

  it('lists the files by the name the person knows', async () => {
    get.mockResolvedValue(
      page([
        { id: 7, display_name: 'Offer.pdf', filename: 'stored-7.pdf' },
        { id: 8, display_name: null, original_name: '/Docs/notes.txt', filename: 'stored-8.txt' },
        { id: 9, filename: 'stored-9.txt' }
      ])
    )
    const wrapper = mountDialog()
    await flushPromises()
    expect(get).toHaveBeenCalledWith('/api/synaplan/from-synaplan?page=1', expect.anything())
    expect(wrapper.get('[data-testid="synaplan-from-file-7"]').text()).toBe('Offer.pdf')
    expect(wrapper.get('[data-testid="synaplan-from-file-8"]').text()).toBe('/Docs/notes.txt')
    expect(wrapper.get('[data-testid="synaplan-from-file-9"]').text()).toBe('stored-9.txt')
  })

  it('copies the chosen file into the folder', async () => {
    get.mockResolvedValue(page([{ id: 7, display_name: 'Offer.pdf' }]))
    post.mockResolvedValue({ data: { path: 'Reports/Offer.pdf' } })
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-from-submit"]').attributes('disabled')).toBeDefined()

    await wrapper.get('[data-testid="synaplan-from-file-7"]').trigger('click')
    await wrapper.get('[data-testid="synaplan-from-submit"]').trigger('click')
    await flushPromises()

    expect(post).toHaveBeenCalledWith(
      '/api/synaplan/from-synaplan',
      { fileId: 7, folderId: 's!folder', name: 'Offer.pdf' },
      expect.anything()
    )
    expect(wrapper.get('[data-testid="synaplan-from-file-7"]').attributes('aria-pressed')).toBe(
      'true'
    )
    expect(wrapper.get('[data-testid="synaplan-from-saved"]').text()).toBe(
      'Copied Offer.pdf into your files. The file is still in Synaplan.'
    )
  })

  it('names the file that was written when the folder already had that name', async () => {
    get.mockResolvedValue(page([{ id: 7, display_name: 'Offer.pdf' }]))
    post.mockResolvedValue({ data: { path: 'Reports/Offer (2).pdf' } })
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-from-file-7"]').trigger('click')
    await wrapper.get('[data-testid="synaplan-from-submit"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-from-saved"]').text()).toContain('Offer (2).pdf')
  })

  it('drops the previous success when the next copy fails', async () => {
    get.mockResolvedValue(page([{ id: 7, display_name: 'Offer.pdf' }]))
    post.mockResolvedValueOnce({ data: { path: 'Reports/Offer.pdf' } })
    post.mockRejectedValueOnce({ response: { data: { code: 'copy_failed' } } })
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-from-file-7"]').trigger('click')
    await wrapper.get('[data-testid="synaplan-from-submit"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-from-submit"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="synaplan-from-saved"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('The file was not copied. It is still in Synaplan.')
  })

  it('says in one sentence why the copy failed', async () => {
    get.mockResolvedValue(page([{ id: 7, display_name: 'Big.zip' }]))
    post.mockRejectedValue({ response: { data: { code: 'file_too_large' } } })
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('[data-testid="synaplan-from-file-7"]').trigger('click')
    await wrapper.get('[data-testid="synaplan-from-submit"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('This file is too large to copy here.')
    expect(wrapper.find('[data-testid="synaplan-from-saved"]').exists()).toBe(false)
  })

  it('says so when there is nothing in Synaplan', async () => {
    get.mockResolvedValue(page([]))
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.text()).toContain('You have no files in Synaplan yet.')
  })

  it('does not call an empty account a failed list', async () => {
    get.mockRejectedValue(new Error('offline'))
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.text()).not.toContain('You have no files in Synaplan yet.')
    expect(wrapper.text()).toContain(
      'Synaplan cannot be reached. Try again in a minute, or ask your administrator.'
    )
  })

  it('loads the next page when the person asks for older files', async () => {
    get.mockImplementation((url: string) => {
      if (url.endsWith('page=2')) {
        return Promise.resolve(page([{ id: 8, display_name: 'Older.txt' }], 2))
      }
      return Promise.resolve(page([{ id: 7, display_name: 'New.txt' }], 2))
    })
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.find('[data-testid="synaplan-from-file-8"]').exists()).toBe(false)
    await wrapper.get('[data-testid="synaplan-from-more"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="synaplan-from-file-8"]').text()).toBe('Older.txt')
    expect(wrapper.find('[data-testid="synaplan-from-more"]').exists()).toBe(false)
  })
})
