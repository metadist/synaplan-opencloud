import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { computed, defineComponent, h, ref } from 'vue'

const post = vi.fn()
const get = vi.fn()
const loadingAddTask = vi.fn(async (fn: () => Promise<unknown>) => fn())

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { post, get } }),
  useLoadingService: () => ({ addTask: loadingAddTask })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s })
}))

vi.mock('../../src/composables/useSynaplanBird', () => ({
  useSynaplanBird: () => computed(() => '/api/synaplan/assets/single_bird-dark.svg')
}))

const stubbedGroups = ref<{ name: string }[]>([])
vi.mock('../../src/composables/useKnowledgeGroups', () => ({
  useKnowledgeGroups: () => ({
    groups: stubbedGroups,
    loading: ref(false),
    error: ref(''),
    reload: vi.fn()
  })
}))

const ocStub = (tag: string) =>
  defineComponent({
    name: tag,
    props: ['modelValue', 'options', 'disabled', 'taggable', 'createOption'],
    emits: ['click', 'update:model-value', 'update:modelValue', 'option:created'],
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

import KnowledgeDialog from '../../src/components/KnowledgeDialog.vue'

function mountDialog() {
  return mount(KnowledgeDialog, {
    props: {
      modal: { id: 'm1' } as never,
      resource: { id: 'res-1', name: 'report.pdf', mimeType: 'application/pdf' }
    },
    global: { stubs: globalStubs }
  })
}

// Helper: drive the inner oc-select stub. We can't actually type into
// vue-select inside a unit test, so we emit the same events vue-select
// would: update:model-value when an existing option is picked, and
// option:created when the user types a new tag.
function pickExistingGroup(wrapper: ReturnType<typeof mountDialog>, name: string) {
  const select = wrapper.findComponent({ name: 'oc-select' })
  return select.vm.$emit('update:model-value', name)
}

function createNewGroup(wrapper: ReturnType<typeof mountDialog>, typed: string) {
  const select = wrapper.findComponent({ name: 'oc-select' })
  // KnowledgeDialog's createOption uppercases + trims; vue-select would
  // run that and emit option:created with the result.
  const normalised = typed.trim().toUpperCase()
  return select.vm.$emit('option:created', normalised)
}

describe('KnowledgeDialog', () => {
  beforeEach(() => {
    post.mockReset()
    get.mockReset()
    loadingAddTask.mockClear()
    stubbedGroups.value = []
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('renders the resource name and the bird logo', () => {
    const wrapper = mountDialog()
    expect(wrapper.text()).toContain('report.pdf')
    expect(wrapper.get('[data-testid="synaplan-knowledge-logo"]').attributes('src')).toBe(
      '/api/synaplan/assets/single_bird-dark.svg'
    )
  })

  it('does not submit when no group has been picked or typed', async () => {
    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-knowledge-submit"]').trigger('click')
    await flushPromises()
    expect(post).not.toHaveBeenCalled()
  })

  it('feeds the loaded groups into the select component', async () => {
    stubbedGroups.value = [{ name: 'DEFAULT' }, { name: 'RESEARCH' }]
    const wrapper = mountDialog()
    await flushPromises()

    const select = wrapper.findComponent({ name: 'oc-select' })
    expect(select.props('options')).toEqual(['DEFAULT', 'RESEARCH'])
  })

  it('feeds an empty options list when no groups are loaded', () => {
    stubbedGroups.value = []
    const wrapper = mountDialog()
    const select = wrapper.findComponent({ name: 'oc-select' })
    expect(select.props('options')).toEqual([])
  })

  it('posts with the picked existing group and shows the success state', async () => {
    stubbedGroups.value = [{ name: 'RESEARCH' }]
    post.mockResolvedValueOnce({
      data: {
        groupKey: 'RESEARCH',
        vectorized: true,
        chunksCreated: 7,
        extractedTextLength: 1234
      }
    })

    const wrapper = mountDialog()
    await flushPromises()
    await pickExistingGroup(wrapper, 'RESEARCH')
    await wrapper.get('[data-testid="synaplan-knowledge-submit"]').trigger('click')
    await flushPromises()

    expect(post).toHaveBeenCalledTimes(1)
    const [url, body] = post.mock.calls[0]
    expect(url).toBe('/api/synaplan/knowledge')
    expect(body).toEqual({ resourceId: 'res-1', groupKey: 'RESEARCH' })

    const success = wrapper.get('[data-testid="synaplan-knowledge-success"]')
    expect(success.text()).toContain('RESEARCH')
    expect(success.text()).toContain('7')
    expect(success.text()).toContain('1234')
    expect(wrapper.find('[data-testid="synaplan-knowledge-submit"]').exists()).toBe(false)
  })

  it('posts with a freshly typed group via the option:created path', async () => {
    post.mockResolvedValueOnce({
      data: { groupKey: 'NEW_ONE', vectorized: true, chunksCreated: 1, extractedTextLength: 0 }
    })

    const wrapper = mountDialog()
    await createNewGroup(wrapper, 'new_one')
    await wrapper.get('[data-testid="synaplan-knowledge-submit"]').trigger('click')
    await flushPromises()

    // KnowledgeDialog uppercases + trims via createOption.
    expect(post.mock.calls[0][1]).toEqual({ resourceId: 'res-1', groupKey: 'NEW_ONE' })
  })

  it('appends a freshly created tag to the option list so it stays selectable', async () => {
    const wrapper = mountDialog()
    await createNewGroup(wrapper, 'BRAND_NEW')
    await flushPromises()

    const select = wrapper.findComponent({ name: 'oc-select' })
    expect(select.props('options')).toContain('BRAND_NEW')
  })

  it('surfaces a backend error', async () => {
    post.mockRejectedValueOnce(new Error('boom'))

    const wrapper = mountDialog()
    await pickExistingGroup(wrapper, 'X')
    await wrapper.get('[data-testid="synaplan-knowledge-submit"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="synaplan-knowledge-error"]').text()).toBe('boom')
    expect(wrapper.find('[data-testid="synaplan-knowledge-success"]').exists()).toBe(false)
  })

  it('falls back to a generic message when the error has no message', async () => {
    post.mockRejectedValueOnce(new Error(''))

    const wrapper = mountDialog()
    await pickExistingGroup(wrapper, 'X')
    await wrapper.get('[data-testid="synaplan-knowledge-submit"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="synaplan-knowledge-error"]').text()).toBe(
      'Knowledge upload failed'
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
    await pickExistingGroup(wrapper, 'X')
    await wrapper.get('[data-testid="synaplan-knowledge-submit"]').trigger('click')
    await flushPromises()

    const exposed = wrapper.vm as unknown as { onCancel: () => void }
    exposed.onCancel()
    rejectPending!(new Error('aborted'))
    await flushPromises()

    expect(wrapper.find('[data-testid="synaplan-knowledge-error"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="synaplan-knowledge-success"]').exists()).toBe(false)
  })
})
