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
      class="ext:w-full ext:rounded ext:border ext:bg-role-surface ext:p-2 ext:text-sm"
      :aria-label="$gettext('Ask a question')"
      :placeholder="$gettext('Ask a question')"
      :disabled="phase === 'loading'"
      data-testid="synaplan-ask-question"
    />
    <oc-button
      appearance="filled"
      color-role="primary"
      :disabled="!question.trim() || phase === 'loading'"
      :show-spinner="phase === 'loading'"
      data-testid="synaplan-ask-submit"
      @click="ask"
    >
      {{ $gettext('Ask') }}
    </oc-button>
    <!-- markdown-it escapes HTML in the answer -->
    <!-- eslint-disable vue/no-v-html -->
    <div
      v-if="phase === 'done'"
      class="ext:rounded ext:border ext:bg-role-surface-container ext:p-3 ext:text-sm"
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
  </section>
</template>

<script setup lang="ts">
import { useGettext } from 'vue3-gettext'
import { useChat } from '../composables/useChat'
import { useRenderedMarkdown } from '../composables/useRenderedMarkdown'

const { $gettext } = useGettext()
const { models, model, question, answer, error, phase, ask, selectModel } = useChat()
const rendered = useRenderedMarkdown(answer)
</script>
