<template>
  <div class="ext:flex ext:flex-col ext:gap-4" data-testid="synaplan-translation-dialog">
    <!-- File being translated -->
    <p class="ext:text-sm ext:text-role-on-surface-variant">
      {{ resource.name }}
    </p>

    <!-- Language picker (hidden once we have a result) -->
    <div v-if="phase !== 'done'">
      <oc-select
        :model-value="selectedLanguage"
        :label="$gettext('Translate to')"
        :options="LANGUAGES"
        :clearable="false"
        :searchable="false"
        :disabled="phase === 'loading'"
        option-label="label"
        data-testid="synaplan-translation-language"
        @update:model-value="onLanguageChange"
      />
    </div>

    <!-- Error display -->
    <div
      v-if="phase === 'error'"
      class="ext:rounded ext:border ext:border-role-error ext:bg-role-error-container ext:p-3 ext:text-sm ext:text-role-on-error-container"
      data-testid="synaplan-translation-error"
    >
      {{ error }}
    </div>

    <!-- Translation result -->
    <div
      v-if="phase === 'done'"
      class="ext:rounded ext:border ext:bg-role-surface-container ext:p-3 ext:text-sm ext:whitespace-pre-wrap ext:max-h-96 ext:overflow-auto"
      data-testid="synaplan-translation-result"
    >
      {{ result }}
    </div>

    <!-- Action bar. The modal's built-in title-bar X handles cancel /
         close for every phase — we only render the primary action
         buttons (Translate while editing, Copy while done). -->
    <div class="ext:flex ext:justify-end ext:gap-2 ext:pt-2">
      <oc-button
        v-if="phase === 'done'"
        appearance="outline"
        data-testid="synaplan-translation-copy"
        @click="copyResult"
      >
        <oc-icon name="file-copy-2" size="small" fill-type="line" />
        {{ $gettext('Copy') }}
      </oc-button>

      <oc-button
        v-if="phase !== 'done'"
        appearance="filled"
        color-role="primary"
        :disabled="phase === 'loading'"
        :show-spinner="phase === 'loading'"
        data-testid="synaplan-translation-submit"
        @click="translate"
      >
        {{ phase === 'loading' ? $gettext('Translating…') : $gettext('Translate') }}
      </oc-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useClientService, useLoadingService, useMessages, type Modal } from '@opencloud-eu/web-pkg'
import { useClipboard } from '@vueuse/core'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'

// The dialog is mounted by the modal system via dispatchModal({
// customComponent: TranslationDialog, customComponentAttrs: () => ({ resource }) }).
// `modal` is auto-injected by the modal host; `resource` is forwarded
// from customComponentAttrs.
const props = defineProps<{
  modal: Modal
  resource: {
    id: string
    name?: string
    mimeType?: string
  }
}>()

const { $gettext } = useGettext()
const { httpAuthenticated } = useClientService()
const { showMessage, showErrorMessage } = useMessages()
const { copy: copyToClipboard } = useClipboard()
const loadingService = useLoadingService()

// Matches the allowlist the backend enforces in
// internal/handler/translate.go. Keep in sync.
type LanguageOption = { id: string; label: string }
const LANGUAGES: LanguageOption[] = [
  { id: 'en', label: 'English' },
  { id: 'de', label: 'Deutsch' },
  { id: 'fr', label: 'Français' },
  { id: 'es', label: 'Español' },
  { id: 'it', label: 'Italiano' }
]

type Phase = 'select' | 'loading' | 'done' | 'error'

const selectedLanguage = ref<LanguageOption>(LANGUAGES[0])
const phase = ref<Phase>('select')
const result = ref('')
const error = ref('')

// Holds the in-flight translate request's AbortController while a
// translation is running, so the modal's cancel/close action can
// abort it and release the loadingService task.
let inFlight: AbortController | null = null

const translateResponseSchema = z.object({
  translation: z.string()
})

function onLanguageChange(value: LanguageOption | null) {
  if (value) {
    selectedLanguage.value = value
  }
}

async function translate() {
  phase.value = 'loading'
  error.value = ''
  result.value = ''

  const controller = new AbortController()
  inFlight = controller

  try {
    // Wrap the POST in loadingService.addTask so OC shows its global
    // loading indicator while the LLM call is in flight. Matches the
    // pattern the rest of web-pkg uses for long-running user-
    // triggered operations.
    const translation = await loadingService.addTask(async () => {
      const { data } = await httpAuthenticated.post(
        '/api/synaplan/translate',
        {
          resourceId: props.resource.id,
          targetLanguage: selectedLanguage.value.id
        },
        {
          schema: translateResponseSchema,
          signal: controller.signal,
          // Translation is LLM-backed and can take minutes for long
          // documents. The backend bounds each request at 10 minutes
          // via context.WithTimeout — let that govern the deadline.
          timeout: 0
        }
      )
      return data.translation
    })
    result.value = translation
    phase.value = 'done'
  } catch (e) {
    if (controller.signal.aborted) {
      // User closed the modal mid-flight. The component is about to
      // unmount anyway; don't bother painting an error state.
      return
    }
    console.error('synaplan translate failed', e)
    error.value = e instanceof Error && e.message ? e.message : $gettext('Translation failed')
    phase.value = 'error'
  } finally {
    if (inFlight === controller) {
      inFlight = null
    }
  }
}

// Called by the OC modal host when the user clicks the title-bar X or
// otherwise cancels the modal. Abort any in-flight translate request
// so the loadingService task drains and the global indicator clears
// immediately instead of limping along until the backend responds.
function onCancel() {
  inFlight?.abort()
}

defineExpose({ onCancel })

async function copyResult() {
  try {
    await copyToClipboard(result.value)
    showMessage({ title: $gettext('Translation copied to your clipboard.') })
  } catch (e) {
    console.error('clipboard write failed', e)
    showErrorMessage({
      title: $gettext('Could not copy translation'),
      errors: [e as Error]
    })
  }
}
</script>
