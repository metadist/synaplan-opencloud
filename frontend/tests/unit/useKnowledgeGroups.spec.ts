import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'

const get = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { get } })
}))

import { useKnowledgeGroups } from '../../src/composables/useKnowledgeGroups'

// The composable uses onMounted, so we exercise it inside a mounted
// component shell rather than calling it bare.
function mountHost() {
  const host = defineComponent({
    setup() {
      const state = useKnowledgeGroups()
      return { state }
    },
    render() {
      return h('div')
    }
  })
  return mount(host)
}

describe('useKnowledgeGroups', () => {
  beforeEach(() => {
    get.mockReset()
    vi.spyOn(console, 'warn').mockImplementation(() => {})
  })

  it('loads groups from /api/synaplan/knowledge/groups on mount', async () => {
    get.mockResolvedValueOnce({
      data: {
        groups: [
          { name: 'DEFAULT', file_count: 3 },
          { name: 'RESEARCH', file_count: 0 }
        ]
      }
    })

    const wrapper = mountHost()
    await flushPromises()

    expect(get).toHaveBeenCalledWith('/api/synaplan/knowledge/groups', expect.any(Object))
    const { state } = wrapper.vm as unknown as {
      state: { groups: { value: { name: string }[] }; loading: { value: boolean } }
    }
    expect(state.groups.value).toHaveLength(2)
    expect(state.groups.value[0].name).toBe('DEFAULT')
    expect(state.loading.value).toBe(false)
  })

  it('swallows fetch failures and exposes the error without throwing', async () => {
    get.mockRejectedValueOnce(new Error('network down'))

    const wrapper = mountHost()
    await flushPromises()

    const { state } = wrapper.vm as unknown as {
      state: {
        groups: { value: unknown[] }
        error: { value: string }
        loading: { value: boolean }
      }
    }
    expect(state.groups.value).toEqual([])
    expect(state.error.value).toBe('network down')
    expect(state.loading.value).toBe(false)
  })
})
