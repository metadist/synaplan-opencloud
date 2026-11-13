<template>
  <section class="ext:space-y-3" data-testid="synaplan-settings">
    <h2 class="ext:text-lg ext:font-semibold">{{ $gettext('Your Synaplan') }}</h2>
    <p v-if="login" class="ext:text-sm" data-testid="synaplan-settings-login">
      {{
        login === 'person'
          ? $gettext('Your OpenCloud login is your Synaplan login.')
          : $gettext('This OpenCloud uses one shared Synaplan account for everyone.')
      }}
    </p>
    <fieldset v-if="scope" class="ext:space-y-2">
      <legend class="ext:text-sm ext:font-semibold">
        {{ $gettext('What Synaplan may read') }}
      </legend>
      <label
        v-for="option in options"
        :key="option.mode"
        class="ext:flex ext:items-center ext:gap-2 ext:text-sm"
      >
        <input
          v-model="mode"
          type="radio"
          name="synaplan-read-mode"
          :value="option.mode"
          :data-testid="`synaplan-settings-read-${option.mode}`"
        />
        {{ option.label }}
      </label>
      <template v-if="mode === 'allowed'">
        <ul v-if="folders.length" class="ext:space-y-1 ext:pl-6 ext:text-sm">
          <li
            v-for="folder in folders"
            :key="folder.id"
            class="ext:flex ext:items-center ext:gap-2"
            data-testid="synaplan-settings-folder"
          >
            <span>{{ folder.path || folder.id }}</span>
            <oc-button
              appearance="raw"
              :aria-label="$gettext('Remove')"
              data-testid="synaplan-settings-folder-remove"
              @click="remove(folder.id)"
            >
              {{ $gettext('Remove') }}
            </oc-button>
          </li>
        </ul>
        <p v-else class="ext:pl-6 ext:text-sm ext:text-role-on-surface-variant">
          {{ $gettext('No folder yet.') }}
        </p>
      </template>
      <oc-button
        appearance="filled"
        color-role="primary"
        :disabled="busy"
        :show-spinner="busy"
        data-testid="synaplan-settings-save"
        @click="saveScope"
      >
        {{ $gettext('Save') }}
      </oc-button>
      <p v-if="saved" class="ext:text-sm" data-testid="synaplan-settings-saved">
        {{ $gettext('Saved. Synaplan follows this setting.') }}
      </p>
    </fieldset>
    <p v-if="error" class="ext:text-sm ext:text-role-error" data-testid="synaplan-settings-error">
      {{ error }}
    </p>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useGettext } from 'vue3-gettext'
import {
  type Login,
  type ReadFolder,
  type ReadMode,
  type ReadScope,
  useReadScope
} from '../composables/useReadScope'
import { sentenceFor } from '../failures'

const { $gettext } = useGettext()
const { login: loadLogin, load, save } = useReadScope()

const login = ref<Login | null>(null)
const scope = ref<ReadScope | null>(null)
const mode = ref<ReadMode>('watched')
const folders = ref<ReadFolder[]>([])
const busy = ref(false)
const saved = ref(false)
const error = ref('')

const options = computed<{ mode: ReadMode; label: string }[]>(() => [
  { mode: 'watched', label: $gettext('Only what I watch') },
  { mode: 'allowed', label: $gettext('Also folders I allow') },
  { mode: 'all', label: $gettext('All of my files') }
])

function show(next: ReadScope) {
  scope.value = next
  mode.value = next.mode
  folders.value = [...next.folders]
}

function remove(id: string) {
  folders.value = folders.value.filter((folder) => folder.id !== id)
  saved.value = false
}

onMounted(async () => {
  try {
    const [who, answer] = await Promise.all([loadLogin(), load()])
    login.value = who
    if (answer.available && answer.readScope) {
      show(answer.readScope)
    }
  } catch (e) {
    error.value = sentenceFor(
      $gettext,
      e,
      $gettext('Synaplan cannot be reached. Try again in a minute, or ask your administrator.')
    )
  }
})

async function saveScope() {
  busy.value = true
  saved.value = false
  error.value = ''
  try {
    show(await save({ mode: mode.value, folders: folders.value }))
    saved.value = true
  } catch (e) {
    error.value = sentenceFor(
      $gettext,
      e,
      $gettext('Synaplan did not receive this setting, so nothing changed. Try again in a minute.')
    )
  } finally {
    busy.value = false
  }
}
</script>
