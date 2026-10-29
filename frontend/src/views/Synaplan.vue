<template>
  <div
    class="ext:flex ext:h-full ext:items-start ext:justify-center ext:overflow-auto ext:p-8 ext:pt-24"
  >
    <div class="ext:w-full ext:max-w-xl ext:space-y-6">
      <h1
        class="ext:text-2xl ext:font-bold ext:flex ext:items-center ext:gap-3"
        data-testid="synaplan-title"
      >
        <img
          :src="birdSrc"
          alt=""
          class="ext:h-8 ext:w-8 ext:shrink-0"
          data-testid="synaplan-title-logo"
        />
        <a
          v-if="synaplanUrl"
          :href="synaplanUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="hover:ext:underline"
          data-testid="synaplan-title-link"
        >
          Synaplan
        </a>
        <template v-else>Synaplan</template>
      </h1>

      <p class="ext:text-sm ext:text-role-on-surface-variant">
        Test the connection to Synaplan. With a shared login provider this OpenCloud account is
        used. A shared API key uses one Synaplan account for everyone.
      </p>

      <p v-if="modelsConfigUrl" class="ext:text-sm">
        <a
          :href="modelsConfigUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="hover:ext:underline"
          data-testid="synaplan-models-link"
        >
          Manage AI models →
        </a>
      </p>

      <oc-button
        appearance="filled"
        color-role="primary"
        :disabled="loading"
        :show-spinner="loading"
        data-testid="synaplan-test-btn"
        @click="testConnection"
      >
        Test Connection
      </oc-button>

      <div
        v-if="result?.account?.email"
        class="ext:rounded ext:border ext:bg-role-surface-container ext:p-4 ext:text-sm"
        data-testid="synaplan-account"
      >
        {{ $gettext('Signed in to Synaplan as') }} {{ result.account.email }}.
      </div>

      <p v-else-if="result && result.status !== 'ok'" class="ext:text-sm ext:text-role-error">
        {{ $gettext('Synaplan did not accept this OpenCloud login.') }}
      </p>

      <p v-if="error" class="ext:text-sm ext:text-role-error" data-testid="synaplan-error">
        {{ error }}
      </p>

      <section class="ext:space-y-3" data-testid="synaplan-settings">
        <h2 class="ext:text-lg ext:font-semibold">{{ $gettext('Your Synaplan') }}</h2>
        <p class="ext:text-sm ext:text-role-on-surface-variant">
          {{
            $gettext(
              'This OpenCloud login is your Synaplan login. A shared key, when an administrator set one, uses one Synaplan account for everyone.'
            )
          }}
        </p>
        <p v-if="connection" class="ext:text-sm" data-testid="synaplan-connection">
          {{ connectionText }}
        </p>
        <div class="ext:flex ext:flex-wrap ext:gap-2">
          <oc-button
            appearance="outline"
            data-testid="synaplan-load-settings"
            @click="loadSettings"
          >
            {{ $gettext('Show what Synaplan may read') }}
          </oc-button>
          <oc-button appearance="outline" data-testid="synaplan-bind" @click="bindAccount">
            {{ $gettext('Connect this login') }}
          </oc-button>
        </div>
        <div v-if="prefs" class="ext:space-y-2">
          <label class="ext:flex ext:items-center ext:gap-2 ext:text-sm">
            <input v-model="readMode" type="radio" value="watched" />
            {{ $gettext('Only what I watch') }}
          </label>
          <label class="ext:flex ext:items-center ext:gap-2 ext:text-sm">
            <input v-model="readMode" type="radio" value="allowed" />
            {{ $gettext('Also folders I allow') }}
          </label>
          <label class="ext:flex ext:items-center ext:gap-2 ext:text-sm">
            <input v-model="readMode" type="radio" value="all" />
            {{ $gettext('All of my files') }}
          </label>
          <oc-button
            appearance="filled"
            color-role="primary"
            data-testid="synaplan-save-read"
            @click="saveRead"
          >
            {{ $gettext('Save') }}
          </oc-button>
          <p v-if="savedRead" class="ext:text-sm">
            {{ $gettext('Saved. Synaplan will follow this setting.') }}
          </p>
        </div>
      </section>

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
        <p v-if="askError" class="ext:text-sm ext:text-role-error">{{ askError }}</p>
        <h2 class="ext:text-lg ext:font-semibold">{{ $gettext('Make an image') }}</h2>
        <textarea
          v-model="imagePrompt"
          rows="2"
          class="ext:w-full ext:rounded ext:border ext:bg-role-surface ext:p-2 ext:text-sm"
          :placeholder="$gettext('Describe an image')"
          data-testid="synaplan-image-prompt"
        />
        <oc-button appearance="outline" data-testid="synaplan-image-make" @click="makeImage">
          {{ $gettext('Make an image') }}
        </oc-button>
        <p v-if="imageNote" class="ext:text-sm" data-testid="synaplan-image-note">
          {{ imageNote }}
        </p>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useGettext } from 'vue3-gettext'
import { useClientService } from '@opencloud-eu/web-pkg'
import { z } from 'zod'
import { useSynaplanBird } from '../composables/useSynaplanBird'
import { useRenderedMarkdown } from '../composables/useRenderedMarkdown'
import { sentenceFor } from '../failures'

const props = defineProps<{
  synaplanUrl?: string
}>()

const birdSrc = useSynaplanBird()

const modelsConfigUrl = computed(() =>
  props.synaplanUrl ? `${props.synaplanUrl.replace(/\/+$/, '')}/config/ai-models` : ''
)

const { $gettext } = useGettext()

const healthSchema = z.object({
  status: z.string(),
  timestamp: z.string().optional(),
  synaplanUrl: z.string().optional(),
  userId: z.string().optional(),
  account: z
    .object({
      email: z.string().optional(),
      firstName: z.string().optional(),
      level: z.string().optional()
    })
    .optional(),
  synaplanResponse: z.string().optional(),
  error: z.string().optional()
})

type HealthResult = z.infer<typeof healthSchema>

const { httpAuthenticated } = useClientService()

const loading = ref(false)
const result = ref<HealthResult | null>(null)
const error = ref('')

const connection = ref<{
  bound: boolean
  configured: boolean
  serverUrl: string
  publicUrl: string
} | null>(null)
const connectionText = computed(() => {
  if (!connection.value) return ''
  if (!connection.value.configured) {
    return $gettext('Ask your administrator to connect this OpenCloud to Synaplan.')
  }
  return connection.value.bound
    ? $gettext('This login is connected to Synaplan.')
    : $gettext('This login is not connected yet.')
})

const prefs = ref(false)
const readMode = ref('watched')
const readFolders = ref<{ id: string; path: string }[]>([])
const copyFolder = ref('')
const savedRead = ref(false)
const question = ref('')
const answer = ref('')
const askError = ref('')
const imagePrompt = ref('')
const imageNote = ref('')
const rendered = useRenderedMarkdown(answer)

const prefsSchema = z.object({
  readScope: z.object({
    mode: z.string(),
    folders: z.array(z.object({ id: z.string(), path: z.string() })).default([])
  }),
  copyFolder: z.string().default('')
})
const connectionSchema = z.object({
  bound: z.boolean(),
  configured: z.boolean(),
  serverUrl: z.string(),
  publicUrl: z.string()
})

async function loadSettings() {
  savedRead.value = false
  try {
    const [prefRes, connRes] = await Promise.all([
      httpAuthenticated.get('/api/synaplan/preferences', { schema: prefsSchema }),
      httpAuthenticated.get('/api/synaplan/connection', { schema: connectionSchema })
    ])
    readMode.value = prefRes.data.readScope.mode || 'watched'
    readFolders.value = prefRes.data.readScope.folders
    copyFolder.value = prefRes.data.copyFolder
    prefs.value = true
    connection.value = connRes.data
  } catch (e) {
    error.value = sentenceFor(
      e,
      $gettext('Synaplan cannot be reached. Try again in a minute, or ask your administrator.')
    )
  }
}

async function saveRead() {
  try {
    await httpAuthenticated.put(
      '/api/synaplan/preferences',
      { mode: readMode.value, folders: readFolders.value, copyFolder: copyFolder.value },
      { schema: z.object({ saved: z.boolean() }) }
    )
    savedRead.value = true
  } catch (e) {
    error.value = sentenceFor(
      e,
      $gettext('Synaplan could not finish this. Try again in a minute, or ask your administrator.')
    )
  }
}

async function bindAccount() {
  try {
    await httpAuthenticated.post(
      '/api/synaplan/connection/bind',
      {},
      { schema: z.object({ bound: z.boolean() }) }
    )
    await loadSettings()
  } catch (e) {
    error.value = sentenceFor(
      e,
      $gettext('Synaplan could not finish this. Try again in a minute, or ask your administrator.')
    )
  }
}

async function ask() {
  const text = question.value.trim()
  if (!text) return
  askError.value = ''
  answer.value = ''
  try {
    const res = await httpAuthenticated.post(
      '/api/synaplan/chat',
      { message: text },
      { schema: z.object({ answer: z.string() }), timeout: 0 }
    )
    answer.value = res.data.answer
  } catch (e) {
    askError.value = sentenceFor(
      e,
      $gettext('Synaplan could not finish this. Try again in a minute, or ask your administrator.')
    )
  }
}

async function makeImage() {
  const prompt = imagePrompt.value.trim()
  if (!prompt) return
  imageNote.value = ''
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
      imageNote.value = $gettext(
        'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
      )
      return
    }
    await httpAuthenticated.post(
      '/api/synaplan/media/save',
      { url, name: 'synaplan.png' },
      { schema: z.object({ sentence: z.string() }) }
    )
    imageNote.value = $gettext('Saved into your files. The image is still in Synaplan.')
  } catch (e) {
    imageNote.value = sentenceFor(
      e,
      $gettext('Synaplan could not finish this. Try again in a minute, or ask your administrator.')
    )
  }
}

async function testConnection() {
  loading.value = true
  error.value = ''

  try {
    const { data } = await httpAuthenticated.get('/api/synaplan/me', { schema: healthSchema })
    result.value = data
  } catch (e) {
    console.error('Connection test failed', e)
    error.value = 'Connection test failed — check console for details'
  } finally {
    loading.value = false
  }
}
</script>
