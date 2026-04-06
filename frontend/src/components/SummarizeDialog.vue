<template>
  <div class="ext:flex ext:flex-col ext:gap-4" data-testid="synaplan-summarize-dialog">
    <img
      :src="birdSrc"
      alt=""
      class="ext:h-10 ext:w-10 ext:self-center"
      data-testid="synaplan-summarize-logo"
    />

    <p class="ext:text-sm ext:text-role-on-surface-variant">
      {{ resource.name }}
    </p>

    <div v-if="phase !== 'done'" class="ext:flex ext:flex-col ext:gap-3">
      <oc-select
        :model-value="selectedType"
        :label="$gettext('Summary type')"
        :options="SUMMARY_TYPES"
        :clearable="false"
        :searchable="false"
        :disabled="phase === 'loading'"
        option-label="label"
        data-testid="synaplan-summarize-type"
        @update:model-value="onTypeChange"
      />

      <oc-select
        :model-value="selectedLength"
        :label="$gettext('Length')"
        :options="LENGTHS"
        :clearable="false"
        :searchable="false"
        :disabled="phase === 'loading'"
        option-label="label"
        data-testid="synaplan-summarize-length"
        @update:model-value="onLengthChange"
      />
    </div>

    <div
      v-if="phase === 'error'"
      class="ext:rounded ext:border ext:border-role-error ext:bg-role-error-container ext:p-3 ext:text-sm ext:text-role-on-error-container"
      data-testid="synaplan-summarize-error"
    >
      {{ error }}
    </div>

    <div
      v-if="phase === 'done'"
      class="ext:rounded ext:border ext:bg-role-surface-container ext:p-3 ext:text-sm ext:whitespace-pre-wrap ext:max-h-96 ext:overflow-auto"
      data-testid="synaplan-summarize-result"
    >
      {{ result }}
    </div>

    <div class="ext:flex ext:justify-end ext:gap-2 ext:pt-2">
      <oc-button
        v-if="phase === 'done'"
        appearance="outline"
        data-testid="synaplan-summarize-copy"
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
        data-testid="synaplan-summarize-submit"
        @click="onSubmit"
      >
        {{ phase === 'loading' ? $gettext('Summarizing…') : $gettext('Summarize') }}
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
// SummarizeDialog, customComponentAttrs: () => ({ resource }) }).
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

// Matches supportedSummaryTypes in internal/handler/summary.go.
type SummaryTypeOption = { id: 'abstractive' | 'bullet-points' | 'extractive'; label: string }
const SUMMARY_TYPES: SummaryTypeOption[] = [
  { id: 'abstractive', label: $gettext('Abstractive') },
  { id: 'bullet-points', label: $gettext('Bullet points') },
  { id: 'extractive', label: $gettext('Extractive') }
]

// Matches supportedLengths in internal/handler/summary.go.
type LengthOption = { id: 'short' | 'medium' | 'long'; label: string }
const LENGTHS: LengthOption[] = [
  { id: 'short', label: $gettext('Short') },
  { id: 'medium', label: $gettext('Medium') },
  { id: 'long', label: $gettext('Long') }
]

const selectedType = ref<SummaryTypeOption>(SUMMARY_TYPES[0])
const selectedLength = ref<LengthOption>(LENGTHS[1])

function onTypeChange(value: SummaryTypeOption | null) {
  if (value) selectedType.value = value
}

function onLengthChange(value: LengthOption | null) {
  if (value) selectedLength.value = value
}

const summarizeSchema = z.object({ summary: z.string() })

const { phase, result, error, submit, cancel, copyResult } = useSummaryDialog({
  endpoint: '/api/synaplan/summarize',
  responseSchema: summarizeSchema,
  extractText: (data) => data.summary,
  failedMessage: $gettext('Summarization failed'),
  copiedTitle: $gettext('Summary copied to your clipboard.'),
  copyErrorTitle: $gettext('Could not copy summary')
})

function onSubmit() {
  submit({
    resourceId: props.resource.id,
    summaryType: selectedType.value.id,
    length: selectedLength.value.id
  })
}

defineExpose({ onCancel: cancel })
</script>
