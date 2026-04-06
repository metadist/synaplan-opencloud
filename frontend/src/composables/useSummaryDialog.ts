import { ref } from 'vue'
import { useClientService, useLoadingService, useMessages } from '@opencloud-eu/web-pkg'
import { useClipboard } from '@vueuse/core'
import type { ZodType } from 'zod'

export type SummaryDialogPhase = 'select' | 'loading' | 'done' | 'error'

// Matches OC Web's convention (WebDavDetails.vue): swap the copy
// icon to `check` for 1500ms after a successful copy.
const COPY_FEEDBACK_MS = 1500

export interface SummaryDialogConfig<T> {
  endpoint: string
  responseSchema: ZodType<T>
  extractText: (data: T) => string
  failedMessage: string
  copyErrorTitle: string
}

/**
 * Owns the shared state machine behind the Translate and Summarize
 * dialogs: submit → loading → done/error, AbortController for cancel,
 * and clipboard copy on the result. Each dialog builds its own
 * request body and passes it to submit().
 */
export function useSummaryDialog<T>(config: SummaryDialogConfig<T>) {
  const { httpAuthenticated } = useClientService()
  const { showErrorMessage } = useMessages()
  const { copy: copyToClipboard } = useClipboard()
  const loadingService = useLoadingService()

  const phase = ref<SummaryDialogPhase>('select')
  const result = ref('')
  const error = ref('')
  const justCopied = ref(false)
  let copyFeedbackTimer: ReturnType<typeof setTimeout> | null = null

  let inFlight: AbortController | null = null

  async function submit(body: Record<string, unknown>) {
    phase.value = 'loading'
    error.value = ''
    result.value = ''

    const controller = new AbortController()
    inFlight = controller

    try {
      // loadingService.addTask drives OC's global loading indicator
      // while the LLM call is in flight. The backend bounds each
      // request itself, so we pass timeout: 0 and let it govern.
      const text = await loadingService.addTask(async () => {
        const { data } = await httpAuthenticated.post(config.endpoint, body, {
          schema: config.responseSchema,
          signal: controller.signal,
          timeout: 0
        })
        return config.extractText(data)
      })
      result.value = text
      phase.value = 'done'
    } catch (e) {
      // User closed the modal mid-flight — skip the error paint.
      if (controller.signal.aborted) {
        return
      }
      console.error(`${config.endpoint} failed`, e)
      error.value = e instanceof Error && e.message ? e.message : config.failedMessage
      phase.value = 'error'
    } finally {
      if (inFlight === controller) {
        inFlight = null
      }
    }
  }

  // Called by the modal host's cancel/close action. Aborting the
  // in-flight request drains the loadingService task immediately
  // instead of limping along until the backend responds.
  function cancel() {
    inFlight?.abort()
  }

  async function copyResult() {
    try {
      await copyToClipboard(result.value)
      justCopied.value = true
      if (copyFeedbackTimer) clearTimeout(copyFeedbackTimer)
      copyFeedbackTimer = setTimeout(() => {
        justCopied.value = false
        copyFeedbackTimer = null
      }, COPY_FEEDBACK_MS)
    } catch (e) {
      console.error('clipboard write failed', e)
      showErrorMessage({
        title: config.copyErrorTitle,
        errors: [e as Error]
      })
    }
  }

  return { phase, result, error, justCopied, submit, cancel, copyResult }
}
