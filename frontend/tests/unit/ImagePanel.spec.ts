import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

const post = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { post } })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({
    $gettext: (s: string, params?: Record<string, string>) =>
      s.replace(/%\{(\w+)\}/g, (_, key: string) => params?.[key] ?? '')
  })
}))

import ImagePanel from '../../src/components/ImagePanel.vue'

const ocButton = defineComponent({
  props: ['disabled', 'showSpinner'],
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

function mountPanel() {
  return mount(ImagePanel, { global: { stubs: { 'oc-button': ocButton } } })
}

const generated = {
  data: { url: '/api/v1/files/uploads/04/000/00004/2026/11/m.png', mimeType: 'image/png' }
}

describe('ImagePanel', () => {
  beforeEach(() => {
    post.mockReset()
  })

  it('cannot make an image without a description', async () => {
    const wrapper = mountPanel()
    expect(wrapper.get('[data-testid="synaplan-image-make"]').attributes()).toHaveProperty(
      'disabled'
    )
    await wrapper.get('[data-testid="synaplan-image-prompt"]').setValue('   ')
    expect(wrapper.get('[data-testid="synaplan-image-make"]').attributes()).toHaveProperty(
      'disabled'
    )
  })

  it('makes the image, saves it under a name from the prompt and says where', async () => {
    post.mockResolvedValueOnce(generated).mockResolvedValueOnce({
      data: { path: 'Synaplan/Images/a red square.png' }
    })
    const wrapper = mountPanel()
    await wrapper.get('[data-testid="synaplan-image-prompt"]').setValue(' a red square ')
    await wrapper.get('[data-testid="synaplan-image-make"]').trigger('click')
    await flushPromises()

    expect(post).toHaveBeenNthCalledWith(
      1,
      '/api/synaplan/media',
      { prompt: 'a red square' },
      expect.objectContaining({ timeout: 0 })
    )
    expect(post).toHaveBeenNthCalledWith(
      2,
      '/api/synaplan/media/save',
      { url: generated.data.url, name: 'a red square.png' },
      expect.objectContaining({ timeout: 0 })
    )
    expect(wrapper.get('[data-testid="synaplan-image-saved"]').text()).toBe(
      'Saved to Synaplan/Images/a red square.png. The image is still in Synaplan.'
    )
    expect(wrapper.find('[data-testid="synaplan-image-error"]').exists()).toBe(false)
  })

  it('says in one sentence why no image was made, and saves nothing', async () => {
    post.mockRejectedValueOnce({ response: { data: { code: 'image_unavailable' } } })
    const wrapper = mountPanel()
    await wrapper.get('[data-testid="synaplan-image-prompt"]').setValue('a red square')
    await wrapper.get('[data-testid="synaplan-image-make"]').trigger('click')
    await flushPromises()

    expect(post).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="synaplan-image-error"]').text()).toBe(
      'This Synaplan has no model that makes images. Ask your administrator.'
    )
    expect(wrapper.find('[data-testid="synaplan-image-saved"]').exists()).toBe(false)
  })

  it('falls back to one sentence when saving fails without a code', async () => {
    post
      .mockResolvedValueOnce(generated)
      .mockRejectedValueOnce(new Error('Request failed with status code 502'))
    const wrapper = mountPanel()
    await wrapper.get('[data-testid="synaplan-image-prompt"]').setValue('a red square')
    await wrapper.get('[data-testid="synaplan-image-make"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="synaplan-image-error"]').text()).toBe(
      'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
    )
    expect(
      wrapper.get('[data-testid="synaplan-image-make"]').attributes('disabled')
    ).toBeUndefined()
  })
})
