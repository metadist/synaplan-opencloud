<template>
  <section class="ext:space-y-3" data-testid="synaplan-admin">
    <h2 class="ext:text-lg ext:font-semibold">{{ $gettext('Synaplan for this OpenCloud') }}</h2>
    <p v-if="instanceSentence" class="ext:text-sm" data-testid="synaplan-admin-instance">
      {{ instanceSentence }}
    </p>
    <template v-if="settings">
      <p class="ext:text-sm" data-testid="synaplan-admin-mode">{{ modeSentence }}</p>

      <template v-if="settings.isAdmin">
        <h3 class="ext:text-base ext:font-semibold">{{ $gettext('Answer language') }}</h3>
        <oc-select
          :model-value="selectedLanguage"
          :label="$gettext('Default answer language')"
          :options="settings.languages"
          option-label="name"
          :clearable="false"
          :searchable="false"
          :position-fixed="true"
          data-testid="synaplan-admin-language"
          @update:model-value="onLanguageChange"
        />
        <oc-checkbox
          v-model="useUserLanguage"
          :label="$gettext('Answer in each person’s OpenCloud language when Synaplan supports it')"
          data-testid="synaplan-admin-user-language"
        />
        <div class="ext:flex ext:items-center ext:gap-3">
          <oc-button
            appearance="filled"
            color-role="primary"
            :disabled="saving || !selectedLanguage"
            :show-spinner="saving"
            data-testid="synaplan-admin-save"
            @click="save"
          >
            {{ $gettext('Save') }}
          </oc-button>
          <span v-if="saved" class="ext:text-sm" data-testid="synaplan-admin-saved">
            {{ $gettext('Saved. New answers follow these settings.') }}
          </span>
        </div>
      </template>
      <p
        v-else
        class="ext:text-sm ext:text-role-on-surface-variant"
        data-testid="synaplan-admin-readonly"
      >
        {{ answerLanguageSentence }}
      </p>
    </template>
    <p v-if="error" class="ext:text-sm ext:text-role-error" data-testid="synaplan-admin-error">
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

const languageSchema = z.object({ code: z.string(), name: z.string() })
const settingsSchema = z.object({
  mode: z.enum(['organisation', 'shared']),
  isAdmin: z.boolean(),
  defaultAnswerLanguage: z.string(),
  useUserLanguage: z.boolean(),
  languages: z.array(languageSchema)
})
const instanceSchema = z.object({
  reachable: z.boolean(),
  capabilities: z.array(z.string())
})
const savedSchema = z.object({ saved: z.boolean() })

type Settings = z.infer<typeof settingsSchema>
type LanguageOption = z.infer<typeof languageSchema>
type Instance = z.infer<typeof instanceSchema>

const settings = ref<Settings | null>(null)
const instance = ref<Instance | null>(null)
const selectedLanguage = ref<LanguageOption | null>(null)
const useUserLanguage = ref(true)
const saving = ref(false)
const saved = ref(false)
const error = ref('')

const modeSentence = computed(() =>
  settings.value?.mode === 'shared'
    ? $gettext(
        'Everyone here uses one shared Synaplan account (SYNAPLAN_API_KEY) and sees its files.'
      )
    : $gettext('People here use Synaplan with their own organisation login, shared with Synaplan.')
)

const answerLanguageSentence = computed(() => {
  const current = settings.value
  if (!current) return ''
  const language =
    current.languages.find((l) => l.code === current.defaultAnswerLanguage)?.name ??
    current.defaultAnswerLanguage
  return current.useUserLanguage
    ? $gettext(
        'Answers come in your OpenCloud language when Synaplan supports it, otherwise in %{language}.',
        { language }
      )
    : $gettext('Answers come in %{language}.', { language })
})

const instanceSentence = computed(() => {
  const state = instance.value
  if (!state) return ''
  if (!state.reachable) {
    return $gettext('Synaplan cannot be reached from this OpenCloud.')
  }
  if (!state.capabilities.length) {
    return $gettext('Synaplan answers.')
  }
  return $gettext('Synaplan answers and offers: %{features}.', {
    features: state.capabilities.join(', ')
  })
})

async function loadInstance() {
  try {
    const { data } = await httpAuthenticated.get('/api/synaplan/instance', {
      schema: instanceSchema
    })
    instance.value = data
  } catch {
    instance.value = { reachable: false, capabilities: [] }
  }
}

async function loadSettings() {
  try {
    const { data } = await httpAuthenticated.get('/api/synaplan/settings', {
      schema: settingsSchema
    })
    const loaded: Settings = data
    settings.value = loaded
    selectedLanguage.value =
      loaded.languages.find((l) => l.code === loaded.defaultAnswerLanguage) ?? null
    useUserLanguage.value = loaded.useUserLanguage
  } catch (e) {
    error.value = sentenceFor(
      $gettext,
      e,
      $gettext('Synaplan cannot be reached. Try again in a minute, or ask your administrator.')
    )
  }
}

function onLanguageChange(value: LanguageOption | null) {
  if (value) {
    selectedLanguage.value = value
    saved.value = false
  }
}

async function save() {
  const language = selectedLanguage.value
  if (!language || !settings.value) return
  saving.value = true
  saved.value = false
  error.value = ''
  try {
    await httpAuthenticated.put(
      '/api/synaplan/settings',
      { defaultAnswerLanguage: language.code, useUserLanguage: useUserLanguage.value },
      { schema: savedSchema }
    )
    settings.value = {
      ...settings.value,
      defaultAnswerLanguage: language.code,
      useUserLanguage: useUserLanguage.value
    }
    saved.value = true
  } catch (e) {
    error.value = sentenceFor(
      $gettext,
      e,
      $gettext('The settings were not saved. Try again in a minute.')
    )
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  void loadInstance()
  void loadSettings()
})
</script>
