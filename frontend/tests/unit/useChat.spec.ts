import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent } from 'vue'

const get = vi.fn()
const post = vi.fn()

vi.mock('@opencloud-eu/web-pkg', () => ({
  useClientService: () => ({ httpAuthenticated: { get, post } })
}))

vi.mock('vue3-gettext', () => ({
  useGettext: () => ({ $gettext: (s: string) => s, current: 'de' })
}))

import { useChat } from '../../src/composables/useChat'

function host(resourceId?: string) {
  let chat!: ReturnType<typeof useChat>
  const wrapper = mount(
    defineComponent({
      setup() {
        chat = useChat(resourceId)
        return () => null
      }
    })
  )
  return { chat, wrapper }
}

const models = {
  models: [
    { id: 'gpt-4o', name: 'GPT-4o', service: 'OpenAI' },
    { id: 'local', name: 'Local', service: '' }
  ]
}

describe('useChat', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('lists the default model first and then the models Synaplan offers', async () => {
    get.mockResolvedValue({ data: models })
    const { chat } = host()
    await flushPromises()

    expect(get).toHaveBeenCalledWith('/api/synaplan/models', expect.anything())
    expect(chat.models.value).toEqual([
      { id: '', label: 'Default model' },
      { id: 'gpt-4o', label: 'GPT-4o (OpenAI)' },
      { id: 'local', label: 'Local' }
    ])
    expect(chat.model.value.id).toBe('')
  })

  it('keeps the default model when the list cannot be read', async () => {
    get.mockRejectedValue(new Error('502'))
    const { chat } = host()
    await flushPromises()
    expect(chat.models.value).toEqual([{ id: '', label: 'Default model' }])
  })

  it('asks about a file with the picked model and the interface language', async () => {
    get.mockResolvedValue({ data: models })
    post.mockResolvedValue({ data: { answer: 'Nordwind GmbH' } })
    const { chat } = host('res-1')
    await flushPromises()

    chat.selectModel(chat.models.value[1])
    chat.selectModel(null)
    chat.question.value = '  Who sent it?  '
    await chat.ask()

    expect(post).toHaveBeenCalledWith(
      '/api/synaplan/chat',
      { resourceId: 'res-1', message: 'Who sent it?', model: 'gpt-4o', language: 'de' },
      expect.objectContaining({ timeout: 0 })
    )
    expect(chat.phase.value).toBe('done')
    expect(chat.answer.value).toBe('Nordwind GmbH')
  })

  it('asks in general without a resource', async () => {
    get.mockResolvedValue({ data: { models: [] } })
    post.mockResolvedValue({ data: { answer: 'Hi' } })
    const { chat } = host()
    chat.question.value = 'Hello'
    await chat.ask()

    expect(post.mock.calls[0][1]).toEqual({ message: 'Hello', model: '', language: 'de' })
  })

  it('does not ask an empty question', async () => {
    get.mockResolvedValue({ data: { models: [] } })
    const { chat } = host()
    chat.question.value = '   '
    await chat.ask()
    expect(post).not.toHaveBeenCalled()
    expect(chat.phase.value).toBe('ask')
  })

  it('shows one sentence when Synaplan fails', async () => {
    get.mockResolvedValue({ data: { models: [] } })
    post.mockRejectedValue({ response: { data: { code: 'rate_limited' } } })
    const { chat } = host()
    chat.question.value = 'Hello'
    await chat.ask()

    expect(chat.phase.value).toBe('error')
    expect(chat.error.value).toBe('Synaplan is busy or your limit is reached. Try again later.')
  })

  it('stays quiet when the question is cancelled', async () => {
    get.mockResolvedValue({ data: { models: [] } })
    post.mockImplementation(
      (_url: string, _body: unknown, { signal }: { signal: AbortSignal }) =>
        new Promise((_, reject) =>
          signal.addEventListener('abort', () => reject(new Error('aborted')))
        )
    )
    const { chat } = host()
    chat.question.value = 'Hello'
    const asking = chat.ask()
    expect(chat.phase.value).toBe('loading')
    chat.cancel()
    await asking

    expect(chat.phase.value).toBe('loading')
    expect(chat.error.value).toBe('')
  })
})
