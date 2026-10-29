<template>
  <div class="ext:flex ext:flex-col ext:gap-4" data-testid="synaplan-chat-dialog">
    <p class="ext:text-sm ext:text-role-on-surface-variant">{{ resource.name }}</p>

    <textarea
      v-if="phase !== 'done'"
      v-model="question"
      rows="3"
      class="ext:w-full ext:rounded ext:border ext:bg-role-surface ext:p-2 ext:text-sm"
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

    <p
      v-if="phase === 'error'"
      class="ext:text-sm ext:text-role-error"
      data-testid="synaplan-chat-error"
    >
      {{ error }}
    </p>

    <div class="ext:flex ext:justify-end ext:gap-2">
      <oc-button
        appearance="filled"
        color-role="primary"
        :disabled="!question.trim() || phase === 'loading'"
        :show-spinner="phase === 'loading'"
        data-testid="synaplan-chat-submit"
        @click="onAsk"
      >
        {{ $gettext('Ask') }}
      </oc-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useClientService, type Modal } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'
import { useRenderedMarkdown } from '../composables/useRenderedMarkdown'
import { sentenceFor } from '../failures'

const props = defineProps<{
  modal: Modal
  resource: { id: string; name?: string }
}>()

const { $gettext } = useGettext()
const { httpAuthenticated } = useClientService()
const question = ref('')
const phase = ref<'ask' | 'loading' | 'done' | 'error'>('ask')
const error = ref('')
const answer = ref('')
const rendered = useRenderedMarkdown(answer)

const schema = z.object({ answer: z.string() })

async function onAsk() {
  const text = question.value.trim()
  if (!text) return
  phase.value = 'loading'
  error.value = ''
  try {
    const res = await httpAuthenticated.post(
      '/api/synaplan/chat',
      { resourceId: props.resource.id, message: text },
      { schema, timeout: 0 }
    )
    answer.value = res.data.answer
    phase.value = 'done'
  } catch (e) {
    error.value = sentenceFor(
      e,
      $gettext('Synaplan could not finish this. Try again in a minute, or ask your administrator.')
    )
    phase.value = 'error'
  }
}
</script>
