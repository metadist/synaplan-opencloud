<template>
  <div class="ext:flex ext:flex-col ext:gap-4" data-testid="synaplan-translation-dialog">
    <img
      :src="birdSrc"
      alt=""
      class="ext:h-10 ext:w-10 ext:self-center"
      data-testid="synaplan-translation-logo"
    />

    <p class="ext:text-sm ext:text-role-on-surface-variant">
      {{ resource.name }}
    </p>

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

    <div
      v-if="phase === 'error'"
      class="ext:rounded ext:border ext:border-role-error ext:bg-role-error-container ext:p-3 ext:text-sm ext:text-role-on-error-container"
      data-testid="synaplan-translation-error"
    >
      {{ error }}
    </div>

    <div
      v-if="phase === 'done'"
      class="ext:rounded ext:border ext:bg-role-surface-container ext:p-3 ext:text-sm ext:whitespace-pre-wrap ext:max-h-96 ext:overflow-auto"
      data-testid="synaplan-translation-result"
    >
      {{ result }}
    </div>

    <div class="ext:flex ext:justify-end ext:gap-2 ext:pt-2">
      <oc-button
        v-if="phase === 'done'"
        appearance="outline"
        data-testid="synaplan-translation-copy"
        @click="copyResult"
      >
        <oc-icon :name="justCopied ? 'check' : 'file-copy-2'" size="small" fill-type="line" />
        {{ justCopied ? $gettext('Copied') : $gettext('Copy') }}
      </oc-button>

      <oc-button
        v-if="phase !== 'done'"
        appearance="filled"
        color-role="primary"
        :disabled="phase === 'loading'"
        :show-spinner="phase === 'loading'"
        data-testid="synaplan-translation-submit"
        @click="onSubmit"
      >
        {{ phase === 'loading' ? $gettext('Translating…') : $gettext('Translate') }}
      </oc-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { type Modal } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'
import { useSynaplanBird } from '../composables/useSynaplanBird'
import { useSummaryDialog } from '../composables/useSummaryDialog'

// Mounted by the modal system via dispatchModal({ customComponent:
// TranslationDialog, customComponentAttrs: () => ({ resource }) }).
const props = defineProps<{
  modal: Modal
  resource: {
    id: string
    name?: string
    mimeType?: string
  }
}>()

const { $gettext } = useGettext()
const birdSrc = useSynaplanBird()

// Matches the allowlist enforced in internal/handler/translate.go.
type LanguageOption = { id: string; label: string }
const LANGUAGES: LanguageOption[] = [
  { id: 'en', label: 'English' },
  { id: 'de', label: 'Deutsch' },
  { id: 'fr', label: 'Français' },
  { id: 'es', label: 'Español' },
  { id: 'it', label: 'Italiano' }
]

const selectedLanguage = ref<LanguageOption>(LANGUAGES[0])

function onLanguageChange(value: LanguageOption | null) {
  if (value) selectedLanguage.value = value
}

const translateSchema = z.object({ translation: z.string() })

const { phase, result, error, justCopied, submit, cancel, copyResult } = useSummaryDialog({
  endpoint: '/api/synaplan/translate',
  responseSchema: translateSchema,
  extractText: (data) => data.translation,
  failedMessage: $gettext('Translation failed'),
  copyErrorTitle: $gettext('Could not copy translation')
})

function onSubmit() {
  submit({
    resourceId: props.resource.id,
    targetLanguage: selectedLanguage.value.id
  })
}

defineExpose({ onCancel: cancel })
</script>
