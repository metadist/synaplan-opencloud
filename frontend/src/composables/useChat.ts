import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useClientService, useMessages } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'
import { sentenceFor } from '../failures'

export const MAX_CHAT_QUESTION = 8000

export type ChatPhase = 'ask' | 'loading' | 'done' | 'error'

export type ModelOption = { id: string; label: string }

const modelsSchema = z.object({
  models: z.array(z.object({ id: z.string(), name: z.string(), service: z.string() }))
})

type ChatModel = z.infer<typeof modelsSchema>['models'][number]

const chatSchema = z.object({ answer: z.string() })

export function formatModelLabel(m: { name: string; service: string }): string {
  return m.service ? `${m.name} (${m.service})` : m.name
}

/**
 * One question to Synaplan through the Go service's /api/synaplan/chat,
 * about a file when `resourceId` is given, otherwise in general. Used by
 * the "Ask about this file" dialog behind the file context menu and the
 * ask panel on the Synaplan app page. The model list comes from
 * /api/synaplan/models; the first option leaves the choice to the
 * person's default model in Synaplan. The answer comes in the person's
 * OpenCloud interface language.
 */
export function useChat(resourceId?: string) {
  const gettext = useGettext()
  const { $gettext } = gettext
  const { httpAuthenticated } = useClientService()
  const messages = useMessages()

  const defaultModel: ModelOption = { id: '', label: $gettext('Default model') }
  const models = ref<ModelOption[]>([defaultModel])
  const model = ref<ModelOption>(defaultModel)
  const question = ref('')
  const answer = ref('')
  const error = ref('')
  const phase = ref<ChatPhase>('ask')
  const justCopied = ref(false)
  const saving = ref(false)
  const savedPath = ref('')

  let inFlight: AbortController | null = null
  let copyResetTimer: ReturnType<typeof setTimeout> | null = null

  const questionTooLong = computed(
    () => Array.from(question.value.trim()).length > MAX_CHAT_QUESTION
  )

  async function loadModels() {
    try {
      const { data } = await httpAuthenticated.get('/api/synaplan/models', {
        schema: modelsSchema
      })
      models.value = [
        defaultModel,
        ...data.models.map((m: ChatModel) => ({
          id: m.id,
          label: formatModelLabel(m)
        }))
      ]
    } catch (e) {
      // Asking still works with the default model.
      console.error('/api/synaplan/models failed', e)
    }
  }

  async function ask() {
    const message = question.value.trim()
    if (!message || questionTooLong.value || phase.value === 'loading') return
    phase.value = 'loading'
    error.value = ''
    answer.value = ''
    savedPath.value = ''
    justCopied.value = false
    const controller = new AbortController()
    inFlight = controller
    try {
      const { data } = await httpAuthenticated.post(
        '/api/synaplan/chat',
        {
          ...(resourceId ? { resourceId } : {}),
          message,
          model: model.value.id,
          language: gettext.current
        },
        { schema: chatSchema, signal: controller.signal, timeout: 0 }
      )
      answer.value = data.answer
      phase.value = 'done'
    } catch (e) {
      if (controller.signal.aborted) return
      console.error('/api/synaplan/chat failed', e)
      error.value = sentenceFor(
        $gettext,
        e,
        $gettext(
          'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
        )
      )
      phase.value = 'error'
    } finally {
      if (inFlight === controller) inFlight = null
    }
  }

  async function copyAnswer() {
    if (!answer.value) return
    try {
      await navigator.clipboard.writeText(answer.value)
      justCopied.value = true
      if (copyResetTimer) clearTimeout(copyResetTimer)
      copyResetTimer = setTimeout(() => {
        justCopied.value = false
      }, 2000)
    } catch (e) {
      console.error('clipboard write failed', e)
      messages?.showErrorMessage?.({ title: $gettext('Could not copy answer') })
    }
  }

  async function saveAnswer(filename: string) {
    if (!answer.value || saving.value) return
    saving.value = true
    try {
      const { data } = await httpAuthenticated.post(
        '/api/synaplan/files',
        { filename, text: answer.value, ...(resourceId ? { resourceId } : {}) },
        { schema: z.object({ path: z.string() }) }
      )
      savedPath.value = data.path
    } catch (e) {
      console.error('synaplan save failed', e)
      messages?.showErrorMessage?.({
        title: $gettext('Could not save answer'),
        desc: sentenceFor($gettext, e, $gettext('Saving failed'))
      })
    } finally {
      saving.value = false
    }
  }

  function cancel() {
    inFlight?.abort()
    if (copyResetTimer) {
      clearTimeout(copyResetTimer)
      copyResetTimer = null
    }
  }

  function selectModel(value: ModelOption | null) {
    if (value) model.value = value
  }

  onMounted(loadModels)
  onBeforeUnmount(cancel)

  return {
    models,
    model,
    question,
    questionTooLong,
    answer,
    error,
    phase,
    justCopied,
    saving,
    savedPath,
    ask,
    copyAnswer,
    saveAnswer,
    cancel,
    selectModel
  }
}
