<template>
  <div class="ext:flex ext:flex-col ext:gap-4" data-testid="synaplan-chat-dialog">
    <p class="ext:text-sm ext:text-role-on-surface-variant">{{ resource.name }}</p>

    <oc-select
      :model-value="model"
      :label="$gettext('Model')"
      :options="models"
      :clearable="false"
      :disabled="phase === 'loading'"
      :position-fixed="true"
      option-label="label"
      data-testid="synaplan-chat-model"
      @update:model-value="selectModel"
    />

    <textarea
      v-model="question"
      rows="3"
      class="ext:w-full ext:rounded ext:border ext:bg-role-surface ext:p-2 ext:text-sm"
      :aria-label="$gettext('Ask about this file')"
      :placeholder="$gettext('Ask about this file')"
      :disabled="phase === 'loading'"
      data-testid="synaplan-chat-question"
    />

    <p
      v-if="phase === 'loading'"
      class="ext:text-sm ext:text-role-on-surface-variant"
      data-testid="synaplan-chat-loading"
    >
      {{ $gettext('Synaplan is writing the answer.') }}
    </p>

    <!-- markdown-it escapes HTML in the answer -->
    <!-- eslint-disable vue/no-v-html -->
    <div
      v-if="phase === 'done'"
      class="synaplan-md ext:rounded ext:border ext:bg-role-surface-container ext:p-3 ext:text-sm ext:max-h-96 ext:overflow-auto"
      data-testid="synaplan-chat-result"
      v-html="rendered"
    />
    <!-- eslint-enable vue/no-v-html -->

    <div
      v-if="phase === 'error'"
      class="ext:rounded ext:border ext:border-role-error ext:bg-role-error-container ext:p-3 ext:text-sm ext:text-role-on-error-container"
      data-testid="synaplan-chat-error"
    >
      {{ error }}
    </div>

    <div class="ext:flex ext:justify-end ext:gap-2">
      <oc-button
        appearance="filled"
        color-role="primary"
        :disabled="!question.trim() || phase === 'loading'"
        :show-spinner="phase === 'loading'"
        data-testid="synaplan-chat-submit"
        @click="ask"
      >
        {{ $gettext('Ask') }}
      </oc-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { type Modal } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { useChat } from '../composables/useChat'
import { useRenderedMarkdown } from '../composables/useRenderedMarkdown'

const props = defineProps<{
  modal: Modal
  resource: { id: string; name?: string }
}>()

const { $gettext } = useGettext()
const { models, model, question, answer, error, phase, ask, cancel, selectModel } = useChat(
  props.resource.id
)
const rendered = useRenderedMarkdown(answer)

defineExpose({ onCancel: cancel })
</script>

<style scoped>
.synaplan-md :deep(p) {
  margin: 0.35em 0;
}
.synaplan-md :deep(h1),
.synaplan-md :deep(h2),
.synaplan-md :deep(h3) {
  font-size: 1rem;
  font-weight: 600;
  margin: 0.6em 0 0.25em;
}
.synaplan-md :deep(ul),
.synaplan-md :deep(ol) {
  margin: 0.35em 0;
  padding-left: 1.2em;
}
</style>
