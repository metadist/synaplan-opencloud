<template>
  <section class="ext:space-y-3" data-testid="synaplan-research">
    <h2 class="ext:text-lg ext:font-semibold">{{ $gettext('Ask Synaplan') }}</h2>
    <textarea
      v-model="question"
      rows="3"
      class="ext:w-full ext:rounded ext:border ext:bg-role-surface ext:p-2 ext:text-sm"
      :placeholder="$gettext('Ask a question')"
      data-testid="synaplan-research-question"
    />
    <oc-button
      appearance="filled"
      color-role="primary"
      :disabled="asking || !question.trim()"
      :show-spinner="asking"
      data-testid="synaplan-research-ask"
      @click="ask"
    >
      {{ $gettext('Ask') }}
    </oc-button>
    <!-- markdown-it escapes HTML in the answer -->
    <!-- eslint-disable vue/no-v-html -->
    <div
      v-if="answer"
      class="ext:rounded ext:border ext:bg-role-surface-container ext:p-3 ext:text-sm"
      data-testid="synaplan-research-answer"
      v-html="rendered"
    />
    <!-- eslint-enable vue/no-v-html -->
    <p
      v-if="askError"
      class="ext:text-sm ext:text-role-error"
      data-testid="synaplan-research-error"
    >
      {{ askError }}
    </p>

    <h2 class="ext:text-lg ext:font-semibold">{{ $gettext('Make an image') }}</h2>
    <textarea
      v-model="imagePrompt"
      rows="2"
      class="ext:w-full ext:rounded ext:border ext:bg-role-surface ext:p-2 ext:text-sm"
      :placeholder="$gettext('Describe an image')"
      data-testid="synaplan-image-prompt"
    />
    <oc-button
      appearance="outline"
      :disabled="making || !imagePrompt.trim()"
      :show-spinner="making"
      data-testid="synaplan-image-make"
      @click="makeImage"
    >
      {{ $gettext('Make an image') }}
    </oc-button>
    <p v-if="imageNote" class="ext:text-sm" data-testid="synaplan-image-note">{{ imageNote }}</p>
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useClientService } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'
import { useRenderedMarkdown } from '../composables/useRenderedMarkdown'
import { sentenceFor } from '../failures'

const { $gettext, current } = useGettext()
const { httpAuthenticated } = useClientService()

const question = ref('')
const answer = ref('')
const askError = ref('')
const asking = ref(false)
const imagePrompt = ref('')
const imageNote = ref('')
const making = ref(false)
const rendered = useRenderedMarkdown(answer)

const couldNotFinish = () =>
  $gettext('Synaplan could not finish this. Try again in a minute, or ask your administrator.')

async function ask() {
  const text = question.value.trim()
  if (!text) return
  askError.value = ''
  answer.value = ''
  asking.value = true
  try {
    const res = await httpAuthenticated.post(
      '/api/synaplan/chat',
      { message: text, language: current },
      { schema: z.object({ answer: z.string() }), timeout: 0 }
    )
    answer.value = res.data.answer
  } catch (e) {
    askError.value = sentenceFor($gettext, e, couldNotFinish())
  } finally {
    asking.value = false
  }
}

async function makeImage() {
  const prompt = imagePrompt.value.trim()
  if (!prompt) return
  imageNote.value = ''
  making.value = true
  try {
    const res = await httpAuthenticated.post(
      '/api/synaplan/media',
      { prompt, type: 'image' },
      {
        schema: z.object({
          file: z.object({ url: z.string() }).optional(),
          success: z.boolean().optional()
        }),
        timeout: 0
      }
    )
    const url = res.data.file?.url
    if (!url) {
      imageNote.value = couldNotFinish()
      return
    }
    await httpAuthenticated.post(
      '/api/synaplan/media/save',
      { url, name: 'synaplan.png' },
      { schema: z.object({ sentence: z.string() }) }
    )
    imageNote.value = $gettext('Saved into your files. The image is still in Synaplan.')
  } catch (e) {
    imageNote.value = sentenceFor($gettext, e, couldNotFinish())
  } finally {
    making.value = false
  }
}
</script>
