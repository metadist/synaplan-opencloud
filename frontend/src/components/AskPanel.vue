<template>
  <section class="ext:space-y-3" data-testid="synaplan-ask">
    <h2 class="ext:text-lg ext:font-semibold">{{ $gettext('Ask Synaplan') }}</h2>
    <oc-select
      :model-value="model"
      :label="$gettext('Model')"
      :options="models"
      :clearable="false"
      :disabled="phase === 'loading'"
      option-label="label"
      data-testid="synaplan-ask-model"
      @update:model-value="selectModel"
    />
    <textarea
      v-model="question"
      rows="3"
      :maxlength="MAX_CHAT_QUESTION"
      class="ext:w-full ext:rounded ext:border ext:bg-role-surface ext:p-2 ext:text-sm disabled:ext:opacity-60"
      :aria-label="$gettext('Ask a question')"
      :placeholder="$gettext('Ask a question')"
      :disabled="phase === 'loading'"
      data-testid="synaplan-ask-question"
    />
    <oc-button
      appearance="filled"
      color-role="primary"
      :disabled="!question.trim() || questionTooLong || phase === 'loading'"
      :show-spinner="phase === 'loading'"
      data-testid="synaplan-ask-submit"
      @click="ask"
    >
      {{ $gettext('Ask') }}
    </oc-button>
    <div role="status" aria-live="polite" aria-atomic="true" data-testid="synaplan-ask-status">
      <!-- markdown-it escapes HTML in the answer -->
      <!-- eslint-disable vue/no-v-html -->
      <div
        v-if="phase === 'done'"
        class="synaplan-md ext:rounded ext:border ext:bg-role-surface-container ext:p-3 ext:text-sm"
        data-testid="synaplan-ask-result"
        v-html="rendered"
      />
      <!-- eslint-enable vue/no-v-html -->
      <p
        v-if="phase === 'error'"
        class="ext:text-sm ext:text-role-error"
        data-testid="synaplan-ask-error"
      >
        {{ error }}
      </p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { useGettext } from 'vue3-gettext'
import { MAX_CHAT_QUESTION, useChat } from '../composables/useChat'
import { useRenderedMarkdown } from '../composables/useRenderedMarkdown'

const { $gettext } = useGettext()
const { models, model, question, questionTooLong, answer, error, phase, ask, selectModel } =
  useChat()
const rendered = useRenderedMarkdown(answer)
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
