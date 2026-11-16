<template>
  <section class="ext:space-y-3" data-testid="synaplan-image">
    <h2 class="ext:text-lg ext:font-semibold">{{ $gettext('Make an image') }}</h2>
    <p class="ext:text-sm ext:text-role-on-surface-variant">
      {{
        $gettext(
          'Synaplan makes the image with your default image model and saves it into your files under Synaplan/Images.'
        )
      }}
    </p>
    <textarea
      v-model="prompt"
      rows="2"
      :maxlength="MAX_PROMPT"
      :disabled="busy"
      class="ext:w-full ext:rounded ext:border ext:bg-role-surface ext:p-2 ext:text-sm disabled:ext:opacity-60"
      :placeholder="$gettext('Describe the image')"
      :aria-label="$gettext('Describe the image')"
      data-testid="synaplan-image-prompt"
    />
    <p
      v-if="promptTooLong"
      class="ext:text-xs ext:text-role-error"
      data-testid="synaplan-image-limit"
    >
      {{ $gettext('Shorten the description to 4,000 characters or fewer.') }}
    </p>
    <oc-button
      appearance="filled"
      color-role="primary"
      :disabled="busy || !prompt.trim() || promptTooLong"
      :show-spinner="busy"
      data-testid="synaplan-image-make"
      @click="make"
    >
      {{ $gettext('Make and save') }}
    </oc-button>
    <div role="status" aria-live="polite" aria-atomic="true" data-testid="synaplan-image-status">
      <p v-if="savedPath" class="ext:text-sm" data-testid="synaplan-image-saved">
        {{ $gettext('Saved to %{path}. The image is still in Synaplan.', { path: savedPath }) }}
      </p>
      <p v-if="error" class="ext:text-sm ext:text-role-error" data-testid="synaplan-image-error">
        {{ error }}
      </p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useClientService } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'
import { sentenceFor } from '../failures'
import { imageName } from '../imageName'

const MAX_PROMPT = 4000

const { $gettext } = useGettext()
const { httpAuthenticated } = useClientService()

const prompt = ref('')
const busy = ref(false)
const savedPath = ref('')
const error = ref('')
let inFlight: AbortController | null = null

// Count Unicode code points to match Go's utf8.RuneCountInString.
const promptTooLong = computed(() => Array.from(prompt.value.trim()).length > MAX_PROMPT)

const generatedSchema = z.object({ url: z.string().min(1), mimeType: z.string() })
const savedSchema = z.object({ path: z.string() })

const couldNotFinish = () =>
  $gettext('Synaplan could not finish this. Try again in a minute, or ask your administrator.')

async function make() {
  const text = prompt.value.trim()
  if (!text || promptTooLong.value || busy.value) return
  busy.value = true
  savedPath.value = ''
  error.value = ''
  const controller = new AbortController()
  inFlight = controller
  try {
    const { data: generated } = await httpAuthenticated.post(
      '/api/synaplan/media',
      { prompt: text },
      { schema: generatedSchema, timeout: 0, signal: controller.signal }
    )
    const { data: saved } = await httpAuthenticated.post(
      '/api/synaplan/media/save',
      { url: generated.url, name: imageName(text, generated.url, generated.mimeType) },
      { schema: savedSchema, timeout: 0, signal: controller.signal }
    )
    savedPath.value = saved.path
  } catch (e) {
    if (controller.signal.aborted) return
    error.value = sentenceFor($gettext, e, couldNotFinish())
  } finally {
    if (inFlight === controller) inFlight = null
    busy.value = false
  }
}

onBeforeUnmount(() => {
  inFlight?.abort()
})
</script>
