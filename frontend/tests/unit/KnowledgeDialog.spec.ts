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

// useKnowledgeGroups calls useClientService + onMounted under the
// hood; stub it so we can control the group list directly.
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
    props: ['modelValue', 'options', 'disabled'],
    emits: ['click', 'update:model-value', 'update:modelValue'],
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

// oc-text-input needs special handling so v-model works — stub it
// with an actual <input> that forwards its value through
// update:modelValue.
const ocTextInputStub = defineComponent({
  name: 'oc-text-input',
  props: ['modelValue', 'label', 'descriptionMessage', 'maxlength'],
  emits: ['update:modelValue'],
  setup(props, { emit }) {
    return () =>
      h('input', {
        'data-stub': 'oc-text-input',
        value: props.modelValue,
        onInput: (e: Event) => emit('update:modelValue', (e.target as HTMLInputElement).value)
      })
  }
})

const globalStubs = {
  'oc-text-input': ocTextInputStub,
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

  it('does not submit when the group field is empty', async () => {
    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-knowledge-submit"]').trigger('click')
    await flushPromises()
    expect(post).not.toHaveBeenCalled()
  })

  it('shows the existing groups as clickable chips when any are loaded', async () => {
    stubbedGroups.value = [{ name: 'DEFAULT' }, { name: 'RESEARCH' }]
    const wrapper = mountDialog()
    await flushPromises()

    const chipsHost = wrapper.get('[data-testid="synaplan-knowledge-existing-groups"]')
    expect(chipsHost.text()).toContain('DEFAULT')
    expect(chipsHost.text()).toContain('RESEARCH')
  })

  it('hides the existing-groups chip row when the group list is empty', () => {
    stubbedGroups.value = []
    const wrapper = mountDialog()
    expect(wrapper.find('[data-testid="synaplan-knowledge-existing-groups"]').exists()).toBe(false)
  })

  it('posts with resourceId + groupKey and shows the success state', async () => {
    post.mockResolvedValueOnce({
      data: {
        groupKey: 'MY_GROUP',
        vectorized: true,
        chunksCreated: 7,
        extractedTextLength: 1234
      }
    })

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-knowledge-group-input"]').setValue('MY_GROUP')
    await wrapper.get('[data-testid="synaplan-knowledge-submit"]').trigger('click')
    await flushPromises()

    expect(post).toHaveBeenCalledTimes(1)
    const [url, body] = post.mock.calls[0]
    expect(url).toBe('/api/synaplan/knowledge')
    expect(body).toEqual({ resourceId: 'res-1', groupKey: 'MY_GROUP' })

    const success = wrapper.get('[data-testid="synaplan-knowledge-success"]')
    expect(success.text()).toContain('MY_GROUP')
    expect(success.text()).toContain('7')
    expect(success.text()).toContain('1234')
    expect(wrapper.find('[data-testid="synaplan-knowledge-submit"]').exists()).toBe(false)
  })

  it('trims the group key before submitting', async () => {
    post.mockResolvedValueOnce({
      data: { groupKey: 'MY_GROUP', vectorized: true, chunksCreated: 1, extractedTextLength: 0 }
    })

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-knowledge-group-input"]').setValue('  MY_GROUP  ')
    await wrapper.get('[data-testid="synaplan-knowledge-submit"]').trigger('click')
    await flushPromises()

    expect(post.mock.calls[0][1]).toEqual({ resourceId: 'res-1', groupKey: 'MY_GROUP' })
  })

  it('fills the input when an existing group chip is clicked', async () => {
    stubbedGroups.value = [{ name: 'RESEARCH' }]
    post.mockResolvedValueOnce({
      data: {
        groupKey: 'RESEARCH',
        vectorized: true,
        chunksCreated: 2,
        extractedTextLength: 42
      }
    })

    const wrapper = mountDialog()
    await flushPromises()
    // Click the chip button (first oc-button inside the existing-groups row).
    const chip = wrapper.get(
      '[data-testid="synaplan-knowledge-existing-groups"] [data-stub="oc-button"]'
    )
    await chip.trigger('click')
    await wrapper.get('[data-testid="synaplan-knowledge-submit"]').trigger('click')
    await flushPromises()

    expect(post.mock.calls[0][1]).toEqual({ resourceId: 'res-1', groupKey: 'RESEARCH' })
  })

  it('surfaces a backend error', async () => {
    post.mockRejectedValueOnce(new Error('boom'))

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-knowledge-group-input"]').setValue('X')
    await wrapper.get('[data-testid="synaplan-knowledge-submit"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="synaplan-knowledge-error"]').text()).toBe('boom')
    expect(wrapper.find('[data-testid="synaplan-knowledge-success"]').exists()).toBe(false)
  })

  it('falls back to a generic message when the error has no message', async () => {
    post.mockRejectedValueOnce(new Error(''))

    const wrapper = mountDialog()
    await wrapper.get('[data-testid="synaplan-knowledge-group-input"]').setValue('X')
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
    await wrapper.get('[data-testid="synaplan-knowledge-group-input"]').setValue('X')
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
