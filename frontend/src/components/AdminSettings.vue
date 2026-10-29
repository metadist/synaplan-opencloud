<template>
  <section class="ext:space-y-4" data-testid="synaplan-admin">
    <p
      v-if="!settings && error"
      class="ext:text-sm ext:text-role-error"
      data-testid="synaplan-admin-error"
    >
      {{ error }}
    </p>
    <template v-if="settings">
      <h2 class="ext:text-lg ext:font-semibold">{{ $gettext('This OpenCloud in Synaplan') }}</h2>
      <p class="ext:text-sm" data-testid="synaplan-instance-state">{{ instanceSentence }}</p>
      <p
        v-if="instance && instance.conflicts > 0"
        class="ext:text-sm ext:text-role-error"
        data-testid="synaplan-instance-conflict"
      >
        {{
          $ngettext(
            'Synaplan knows one other cloud for this login, so nothing sent with a person’s login can be attributed to this one. Ask a Synaplan administrator to remove the one no longer in use.',
            'Synaplan knows %{count} other clouds for this login, so nothing sent with a person’s login can be attributed to this one. Ask a Synaplan administrator to remove those no longer in use.',
            instance.conflicts,
            { count: String(instance.conflicts) }
          )
        }}
      </p>
      <oc-button
        v-if="settings.isAdmin && instance && !instance.registered"
        appearance="filled"
        color-role="primary"
        :disabled="registering"
        :show-spinner="registering"
        data-testid="synaplan-instance-register"
        @click="register"
      >
        {{ $gettext('Register this OpenCloud') }}
      </oc-button>
      <h2 class="ext:text-lg ext:font-semibold">
        {{ $gettext('How do people here use Synaplan?') }}
      </h2>
      <div class="ext:space-y-2" data-testid="synaplan-admin-mode">
        <div v-for="option in modes" :key="option.id" class="ext:space-y-1">
          <label
            class="ext:flex ext:items-center ext:gap-2 ext:text-sm"
            :class="{ 'ext:opacity-60': !option.available }"
          >
            <input
              type="radio"
              name="synaplan-mode"
              :value="option.id"
              :checked="settings.mode === option.id"
              :disabled="true"
            />
            {{ option.label }}
          </label>
          <p class="ext:pl-6 ext:text-xs ext:text-role-on-surface-variant">{{ option.hint }}</p>
        </div>
        <p class="ext:text-xs ext:text-role-on-surface-variant">
          {{
            $gettext(
              'The mode is set in the service configuration: a shared key (SYNAPLAN_API_KEY) means one shared account, otherwise the organisation login is used.'
            )
          }}
        </p>
      </div>

      <template v-if="settings.isAdmin">
        <h2 class="ext:text-lg ext:font-semibold">{{ $gettext('Language & AI behaviour') }}</h2>
        <p class="ext:text-sm ext:text-role-on-surface-variant">
          {{
            $gettext(
              'Choose which language the AI answers in, and whether personal memories may be used in chat.'
            )
          }}
        </p>
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
          :label="$gettext('Use each person’s OpenCloud interface language when supported')"
          data-testid="synaplan-admin-user-language"
        />
        <oc-checkbox
          v-model="allowMemories"
          :label="$gettext('Allow personal memories in chat')"
          data-testid="synaplan-admin-memories"
        />
        <p
          v-if="allowMemories && !settings.memoriesAvailable"
          class="ext:text-xs ext:text-role-error"
        >
          {{
            $gettext(
              'Synaplan’s memory service does not answer right now. Memories stay off until it does.'
            )
          }}
        </p>
        <div class="ext:flex ext:items-center ext:gap-3">
          <oc-button
            appearance="filled"
            color-role="primary"
            :disabled="saving"
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
      <p v-if="error" class="ext:text-sm ext:text-role-error" data-testid="synaplan-admin-error">
        {{ error }}
      </p>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useClientService } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'
import { sentenceFor } from '../failures'

const { $gettext, $ngettext } = useGettext()
const { httpAuthenticated } = useClientService()

const languageSchema = z.object({ code: z.string(), name: z.string() })
const settingsSchema = z.object({
  mode: z.string(),
  isAdmin: z.boolean(),
  defaultAnswerLanguage: z.string(),
  useUserLanguage: z.boolean(),
  allowMemories: z.boolean(),
  memoriesAvailable: z.boolean(),
  languages: z.array(languageSchema)
})
type AdminSettingsData = z.infer<typeof settingsSchema>
type LanguageOption = z.infer<typeof languageSchema>

const instanceSchema = z.object({
  registered: z.boolean(),
  status: z.string(),
  host: z.string(),
  conflicts: z.number().default(0)
})
type InstanceState = z.infer<typeof instanceSchema>

const settings = ref<AdminSettingsData | null>(null)
const instance = ref<InstanceState | null>(null)
const registering = ref(false)
const selectedLanguage = ref<LanguageOption | null>(null)
const useUserLanguage = ref(true)
const allowMemories = ref(true)
const saving = ref(false)
const saved = ref(false)
const error = ref('')

// The same four names as the Nextcloud app. Two of them exist only
// there; they are listed so the words match, and say why.
const modes = computed(() => [
  {
    id: 'organisation',
    available: true,
    label: $gettext('Organisation login'),
    hint: $gettext('People sign in once with your identity provider, shared with Synaplan.')
  },
  {
    id: 'link',
    available: false,
    label: $gettext('Connect your Synaplan account'),
    hint: $gettext('Not available on OpenCloud.')
  },
  {
    id: 'provision',
    available: false,
    label: $gettext('Accounts created by the administrator'),
    hint: $gettext('Not available on OpenCloud.')
  },
  {
    id: 'shared',
    available: true,
    label: $gettext('One shared account'),
    hint: $gettext(
      'Everyone acts as one Synaplan account and sees its files. Not for teams with private documents.'
    )
  }
])

const answerLanguageSentence = computed(() => {
  if (!settings.value) return ''
  const name =
    settings.value.languages.find((l) => l.code === settings.value?.defaultAnswerLanguage)?.name ??
    settings.value.defaultAnswerLanguage
  return settings.value.useUserLanguage
    ? $gettext(
        'Answers come in your OpenCloud language when it is supported, otherwise in %{language}.',
        {
          language: name
        }
      )
    : $gettext('Answers come in %{language}.', { language: name })
})

// Who connected this cloud, and whether Synaplan has said yes yet. The
// administrator's own login does the registering; nothing is pasted.
const instanceSentence = computed(() => {
  if (!instance.value) return ''
  if (!instance.value.registered) {
    return settings.value?.isAdmin
      ? $gettext(
          'Not registered yet. Register it with your login: copies, folder reads and watches start working once Synaplan has accepted it.'
        )
      : $gettext('Ask your administrator to connect this OpenCloud to Synaplan.')
  }
  if (instance.value.status === 'active') {
    return $gettext('Registered as %{host} and accepted by Synaplan.', {
      host: instance.value.host
    })
  }
  return $gettext(
    'Registered as %{host}. Waiting for a Synaplan administrator to approve it in Synaplan.',
    { host: instance.value.host }
  )
})

async function loadInstance() {
  try {
    const res = await httpAuthenticated.get('/api/synaplan/instance', { schema: instanceSchema })
    instance.value = res.data
  } catch {
    instance.value = null
  }
}

async function register() {
  registering.value = true
  error.value = ''
  try {
    const res = await httpAuthenticated.post(
      '/api/synaplan/instance/register',
      {},
      { schema: instanceSchema }
    )
    instance.value = res.data
  } catch (e) {
    error.value = sentenceFor(
      $gettext,
      e,
      $gettext('Synaplan could not finish this. Try again in a minute, or ask your administrator.')
    )
  } finally {
    registering.value = false
  }
}

function onLanguageChange(value: LanguageOption | null) {
  if (value) selectedLanguage.value = value
}

onMounted(async () => {
  void loadInstance()
  try {
    const res = await httpAuthenticated.get('/api/synaplan/settings', { schema: settingsSchema })
    const data: AdminSettingsData = res.data
    settings.value = data
    selectedLanguage.value =
      data.languages.find((l: LanguageOption) => l.code === data.defaultAnswerLanguage) ?? null
    useUserLanguage.value = data.useUserLanguage
    allowMemories.value = data.allowMemories
  } catch (e) {
    error.value = sentenceFor(
      $gettext,
      e,
      $gettext('Synaplan cannot be reached. Try again in a minute, or ask your administrator.')
    )
  }
})

async function save() {
  if (!selectedLanguage.value) return
  saving.value = true
  saved.value = false
  error.value = ''
  try {
    await httpAuthenticated.put(
      '/api/synaplan/settings',
      {
        defaultAnswerLanguage: selectedLanguage.value.code,
        useUserLanguage: useUserLanguage.value,
        allowMemories: allowMemories.value
      },
      { schema: z.object({ saved: z.boolean() }) }
    )
    saved.value = true
    if (settings.value) {
      settings.value = {
        ...settings.value,
        defaultAnswerLanguage: selectedLanguage.value.code,
        useUserLanguage: useUserLanguage.value,
        allowMemories: allowMemories.value
      }
    }
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
</script>
