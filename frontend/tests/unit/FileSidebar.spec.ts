import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

const post = vi.fn()
const get = vi.fn()
const del = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { post, get, delete: del } })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

import FileSidebar from '../../src/components/FileSidebar.vue'

const ocButton = defineComponent({
  name: 'oc-button',
  emits: ['click'],
  setup(_, { slots, emit }) {
    return () => h('button', { onClick: () => emit('click') }, slots.default?.())
  }
})

function status(state: string) {
  return {
    data: {
      enabled: true,
      items: [{ id: 's!f1', state, file_id: 9, knowledge_folder: { name: 'Invoices' } }]
    }
  }
}

function mountSidebar() {
  return mount(FileSidebar, {
    props: { resource: { id: 's!f1', etag: '"v1"', name: 'a.txt' } },
    global: { stubs: { 'oc-button': ocButton } }
  })
}

const text = (w: ReturnType<typeof mountSidebar>, id: string) =>
  w.find(`[data-testid="synaplan-sidebar-${id}"]`)

describe('FileSidebar', () => {
  beforeEach(() => {
    post.mockReset()
    get.mockReset()
    del.mockReset()
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('names the state and the knowledge folder of a searchable file', async () => {
    post.mockResolvedValue(status('searchable'))
    const wrapper = mountSidebar()
    await flushPromises()
    expect(text(wrapper, 'state').text()).toBe('Searchable by AI.')
    expect(text(wrapper, 'folder').text()).toContain('Invoices')
    expect(text(wrapper, 'remove').exists()).toBe(true)
    expect(get).not.toHaveBeenCalled()
  })

  it('says so for a file Synaplan does not know', async () => {
    post.mockResolvedValue(status('none'))
    get.mockResolvedValue({ data: { inKnowledge: false, stale: false, synaplanFileId: 0 } })
    const wrapper = mountSidebar()
    await flushPromises()
    expect(text(wrapper, 'not-searchable').exists()).toBe(true)
    expect(text(wrapper, 'remove').exists()).toBe(false)
  })

  it('falls back to the knowledge status when Synaplan keeps no cloud file state', async () => {
    post.mockResolvedValue({ data: { enabled: false, items: [] } })
    get.mockResolvedValue({ data: { inKnowledge: true, stale: true, synaplanFileId: 9 } })
    const wrapper = mountSidebar()
    await flushPromises()
    expect(text(wrapper, 'state').text()).toBe('Changed since it was made searchable.')
    expect(text(wrapper, 'remove').exists()).toBe(true)
  })

  it('removes the file from its knowledge folder', async () => {
    post.mockResolvedValueOnce(status('searchable'))
    del.mockResolvedValue({ data: { removed: true } })
    const wrapper = mountSidebar()
    await flushPromises()
    await text(wrapper, 'remove').trigger('click')
    await flushPromises()
    expect(del).toHaveBeenCalledWith('/api/synaplan/knowledge?resourceId=s!f1', expect.anything())
    expect(text(wrapper, 'message').text()).toBe(
      'Removed from the knowledge folder. It is no longer searchable.'
    )
    expect(text(wrapper, 'state').exists()).toBe(false)
  })

  it('says in one sentence why a removal failed', async () => {
    post.mockResolvedValue(status('searchable'))
    del.mockRejectedValue({ response: { data: { code: 'synaplan_error' } } })
    const wrapper = mountSidebar()
    await flushPromises()
    await text(wrapper, 'remove').trigger('click')
    await flushPromises()
    expect(text(wrapper, 'message').text()).toBe(
      'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
    )
  })
})
