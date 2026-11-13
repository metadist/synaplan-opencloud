import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

const post = vi.fn()
const get = vi.fn()
const del = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { post, get, delete: del } })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({
    $gettext: (s: string) => s,
    $ngettext: (one: string, many: string, n: number, params: Record<string, string>) =>
      (n === 1 ? one : many).replace('%{n}', params.n)
  })
}))

const ocStub = (tag: string) =>
  defineComponent({
    name: tag,
    props: ['modelValue', 'options', 'disabled', 'taggable', 'createOption'],
    emits: ['click', 'update:model-value', 'update:modelValue', 'option:created'],
    setup(props, { slots, emit }) {
      return () =>
        h(
          tag,
          {
            onClick: (e: MouseEvent) => {
              if (!props.disabled) emit('click', e)
            },
            disabled: props.disabled,
            'data-stub': tag
          },
          slots.default?.()
        )
    }
  })

const globalStubs = {
  'oc-select': ocStub('oc-select'),
  'oc-button': ocStub('oc-button')
}

import WatchDialog from '../../src/components/WatchDialog.vue'

const options = (overrides: Record<string, unknown> = {}) => ({
  data: {
    tasks: [{ id: 3, name: 'Contract check' }],
    folders: ['INVOICES'],
    watching: false,
    uploadsEnabled: true,
    ...overrides
  }
})

function mountDialog(kind: 'file' | 'folder' = 'folder') {
  return mount(WatchDialog, {
    props: {
      modal: { id: 'm1' } as never,
      resource: { id: 's$sp!inbox', name: 'Inbox' },
      kind
    },
    global: { stubs: globalStubs }
  })
}

const testid = (id: string) => `[data-testid="synaplan-watch-${id}"]`

describe('WatchDialog', () => {
  beforeEach(() => {
    post.mockReset()
    get.mockReset()
    del.mockReset()
    get.mockResolvedValue(options())
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('asks for the options of this item', async () => {
    mountDialog()
    await flushPromises()
    expect(get).toHaveBeenCalledWith(
      '/api/synaplan/watches/options?targetId=s%24sp!inbox',
      expect.anything()
    )
  })

  it('offers the knowledge folders and starts a searchable watch', async () => {
    post.mockResolvedValueOnce({ data: { id: 7, uploadsEnabled: true } })
    const wrapper = mountDialog()
    await flushPromises()

    const select = wrapper.findComponent({ name: 'oc-select' })
    expect(select.props('options')).toEqual(['INVOICES'])
    expect(wrapper.get(testid('submit')).attributes('disabled')).toBeDefined()

    await select.vm.$emit('option:created', 'RECEIPTS')
    await wrapper.get(testid('submit')).trigger('click')
    await flushPromises()

    expect(post).toHaveBeenCalledWith(
      '/api/synaplan/watches',
      {
        targetKind: 'folder',
        targetId: 's$sp!inbox',
        preset: 'keep_searchable',
        groupKey: 'RECEIPTS',
        taskId: 0
      },
      expect.anything()
    )
    expect(wrapper.find(testid('watching')).exists()).toBe(true)
    expect(wrapper.find(testid('no-uploads')).exists()).toBe(false)
  })

  it('starts a watch that runs a task', async () => {
    post.mockResolvedValueOnce({ data: { id: 8, uploadsEnabled: true } })
    const wrapper = mountDialog('file')
    await flushPromises()

    await wrapper.get(testid('preset-task')).setValue(true)
    await wrapper.findComponent({ name: 'oc-select' }).vm.$emit('update:model-value', {
      id: 3,
      name: 'Contract check'
    })
    await wrapper.get(testid('submit')).trigger('click')
    await flushPromises()

    expect(post).toHaveBeenCalledWith(
      '/api/synaplan/watches',
      expect.objectContaining({ targetKind: 'file', preset: 'task', taskId: 3, groupKey: '' }),
      expect.anything()
    )
  })

  it('cannot pick a task when the person has none', async () => {
    get.mockResolvedValueOnce(options({ tasks: [] }))
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.get(testid('preset-task')).attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('You have no tasks you start yourself.')
  })

  it('says when files are only sent on a check', async () => {
    get.mockResolvedValueOnce(options({ uploadsEnabled: false }))
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.find(testid('no-uploads')).exists()).toBe(true)
  })

  it('checks a watched item now and says what was sent', async () => {
    get.mockResolvedValueOnce(options({ watching: true }))
    post.mockResolvedValueOnce({ data: { watches: 1, sent: 2, failed: 0 } })
    const wrapper = mountDialog()
    await flushPromises()

    expect(wrapper.find(testid('submit')).exists()).toBe(false)
    await wrapper.get(testid('check')).trigger('click')
    await flushPromises()

    expect(post).toHaveBeenCalledWith(
      '/api/synaplan/watches/reconcile?targetId=s%24sp!inbox',
      {},
      expect.objectContaining({ timeout: 0 })
    )
    expect(wrapper.get(testid('checked')).text()).toBe('Checked. 2 files were sent to Synaplan.')
  })

  it.each([
    [{ watches: 1, sent: 0, failed: 0 }, 'Checked. Synaplan has every file already.'],
    [{ watches: 1, sent: 1, failed: 0 }, 'Checked. 1 file was sent to Synaplan.'],
    [
      { watches: 1, sent: 1, failed: 1 },
      'Checked. Some files could not be sent; Synaplan asks for them again later.'
    ]
  ])('reports the check %o', async (result, sentence) => {
    get.mockResolvedValueOnce(options({ watching: true }))
    post.mockResolvedValueOnce({ data: result })
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get(testid('check')).trigger('click')
    await flushPromises()
    expect(wrapper.get(testid('checked')).text()).toBe(sentence)
  })

  it('stops watching', async () => {
    get.mockResolvedValueOnce(options({ watching: true }))
    del.mockResolvedValueOnce({ data: { stopped: 1 } })
    const wrapper = mountDialog()
    await flushPromises()

    await wrapper.get(testid('stop')).trigger('click')
    await flushPromises()

    expect(del).toHaveBeenCalledWith('/api/synaplan/watches?targetId=s%24sp!inbox')
    expect(wrapper.find(testid('stopped')).exists()).toBe(true)
    expect(wrapper.find(testid('check')).exists()).toBe(false)
  })

  it('shows one readable sentence when the options cannot be loaded', async () => {
    get.mockReset()
    get.mockRejectedValueOnce({ response: { data: { code: 'cloud_not_linked' } } })
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.get(testid('error')).text()).toBe(
      'Synaplan does not know this OpenCloud yet. Ask your administrator.'
    )
    expect(wrapper.find(testid('submit')).exists()).toBe(false)
    expect(wrapper.find(testid('loading')).exists()).toBe(false)
  })

  it('shows the fallback when a start fails without a code', async () => {
    post.mockRejectedValueOnce(new Error('Request failed with status code 502'))
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.findComponent({ name: 'oc-select' }).vm.$emit('update:model-value', 'INVOICES')
    await wrapper.get(testid('submit')).trigger('click')
    await flushPromises()
    expect(wrapper.get(testid('error')).text()).toBe(
      'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
    )
    expect(wrapper.find(testid('watching')).exists()).toBe(false)
  })
})
