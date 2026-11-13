import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useClientService } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'
import { sentenceFor } from '../failures'

export type ChatPhase = 'ask' | 'loading' | 'done' | 'error'

export type ModelOption = { id: string; label: string }

const modelsSchema = z.object({
  models: z.array(z.object({ id: z.string(), name: z.string(), service: z.string() }))
})

type ChatModel = z.infer<typeof modelsSchema>['models'][number]

const chatSchema = z.object({ answer: z.string() })

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

  const defaultModel: ModelOption = { id: '', label: $gettext('Default model') }
  const models = ref<ModelOption[]>([defaultModel])
  const model = ref<ModelOption>(defaultModel)
  const question = ref('')
  const answer = ref('')
  const error = ref('')
  const phase = ref<ChatPhase>('ask')

  let inFlight: AbortController | null = null

  async function loadModels() {
    try {
      const { data } = await httpAuthenticated.get('/api/synaplan/models', {
        schema: modelsSchema
      })
      models.value = [
        defaultModel,
        ...data.models.map((m: ChatModel) => ({
          id: m.id,
          label: m.service ? `${m.name} (${m.service})` : m.name
        }))
      ]
    } catch (e) {
      // Asking still works with the default model.
      console.error('/api/synaplan/models failed', e)
    }
  }

  async function ask() {
    const message = question.value.trim()
    if (!message || phase.value === 'loading') return
    phase.value = 'loading'
    error.value = ''
    answer.value = ''
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

  function cancel() {
    inFlight?.abort()
  }

  function selectModel(value: ModelOption | null) {
    if (value) model.value = value
  }

  onMounted(loadModels)
  onBeforeUnmount(cancel)

  return { models, model, question, answer, error, phase, ask, cancel, selectModel }
}
