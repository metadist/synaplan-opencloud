<template>
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
    <div
      v-if="connection?.configured && !connection.bound"
      class="ext:flex ext:flex-wrap ext:gap-2"
    >
      <oc-button
        appearance="filled"
        color-role="primary"
        data-testid="synaplan-bind"
        @click="bindAccount"
      >
        {{ $gettext('Connect this login') }}
      </oc-button>
    </div>
    <p v-if="prefs" class="ext:text-sm ext:font-semibold">
      {{ $gettext('What Synaplan may read') }}
    </p>
    <div v-if="prefs" class="ext:space-y-2">
      <label class="ext:flex ext:items-center ext:gap-2 ext:text-sm">
        <input v-model="readMode" type="radio" value="watched" />
        {{ $gettext('Only what I watch') }}
      </label>
      <label class="ext:flex ext:items-center ext:gap-2 ext:text-sm">
        <input v-model="readMode" type="radio" value="allowed" />
        {{ $gettext('Also folders I allow') }}
      </label>
      <p v-if="readMode === 'allowed' && readFolders.length" class="ext:pl-6 ext:text-sm">
        {{ readFolders.map((folder) => folder.path || folder.id).join(', ') }}
      </p>
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
      <p v-if="savedRead" class="ext:text-sm" data-testid="synaplan-read-saved">
        {{
          savedLocally
            ? $gettext(
                'Saved here only: this OpenCloud is not connected to Synaplan yet. Ask your administrator.'
              )
            : $gettext('Saved. Synaplan will follow this setting.')
        }}
      </p>
    </div>
    <p v-if="error" class="ext:text-sm ext:text-role-error" data-testid="synaplan-account-error">
      {{ error }}
    </p>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useClientService } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'
import { sentenceFor } from '../failures'

const { $gettext } = useGettext()
const { httpAuthenticated } = useClientService()

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

const connection = ref<z.infer<typeof connectionSchema> | null>(null)
const prefs = ref(false)
const readMode = ref('watched')
const readFolders = ref<{ id: string; path: string }[]>([])
const copyFolder = ref('')
const savedRead = ref(false)
const savedLocally = ref(false)
const error = ref('')

const connectionText = computed(() => {
  if (!connection.value) return ''
  if (!connection.value.configured) {
    return $gettext('Ask your administrator to connect this OpenCloud to Synaplan.')
  }
  return connection.value.bound
    ? $gettext('This login is connected to Synaplan.')
    : $gettext('This login is not connected yet.')
})

const unreachable = () =>
  $gettext('Synaplan cannot be reached. Try again in a minute, or ask your administrator.')
const couldNotFinish = () =>
  $gettext('Synaplan could not finish this. Try again in a minute, or ask your administrator.')

onMounted(loadSettings)

async function loadSettings() {
  savedRead.value = false
  savedLocally.value = false
  error.value = ''
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
    error.value = sentenceFor($gettext, e, unreachable())
  }
}

async function saveRead() {
  error.value = ''
  try {
    const res = await httpAuthenticated.put(
      '/api/synaplan/preferences',
      { mode: readMode.value, folders: readFolders.value, copyFolder: copyFolder.value },
      { schema: z.object({ saved: z.boolean(), reported: z.boolean().default(false) }) }
    )
    savedRead.value = true
    savedLocally.value = !res.data.reported
  } catch (e) {
    error.value = sentenceFor(
      $gettext,
      e,
      $gettext(
        'The setting was saved here, but Synaplan did not receive it. Save again in a minute.'
      )
    )
  }
}

async function bindAccount() {
  error.value = ''
  try {
    await httpAuthenticated.post(
      '/api/synaplan/connection/bind',
      {},
      { schema: z.object({ bound: z.boolean() }) }
    )
    await loadSettings()
  } catch (e) {
    error.value = sentenceFor($gettext, e, couldNotFinish())
  }
}
</script>
