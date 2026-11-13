<template>
  <div class="ext:flex ext:flex-col ext:gap-4" data-testid="synaplan-watch-dialog">
    <p class="ext:text-sm ext:text-role-on-surface-variant">
      {{ resource.name }}
    </p>

    <p
      v-if="phase === 'loading'"
      class="ext:text-sm ext:text-role-on-surface-variant"
      data-testid="synaplan-watch-loading"
    >
      {{ $gettext('Loading…') }}
    </p>

    <template v-if="phase === 'setup'">
      <p class="ext:text-sm">
        {{
          kind === 'file'
            ? $gettext('What should happen when this file changes?')
            : $gettext('What should happen with the files in this folder?')
        }}
      </p>

      <label class="ext:flex ext:items-center ext:gap-2 ext:text-sm">
        <input
          v-model="preset"
          type="radio"
          value="keep_searchable"
          data-testid="synaplan-watch-preset-searchable"
        />
        {{ kind === 'file' ? $gettext('Keep it searchable') : $gettext('Keep them searchable') }}
      </label>
      <oc-select
        v-if="preset === 'keep_searchable'"
        :model-value="groupKey"
        :label="$gettext('Knowledge folder')"
        :options="folderOptions"
        :taggable="true"
        :clearable="false"
        :position-fixed="true"
        :create-option="createOption"
        :description-message="
          $gettext('Pick a knowledge folder or type a new name. Synaplan will create it.')
        "
        data-testid="synaplan-watch-folder"
        @update:model-value="groupKey = $event ?? ''"
        @option:created="onFolderCreated"
      />

      <label class="ext:flex ext:items-center ext:gap-2 ext:text-sm">
        <input
          v-model="preset"
          type="radio"
          value="task"
          :disabled="tasks.length === 0"
          data-testid="synaplan-watch-preset-task"
        />
        {{ $gettext('Run one of my tasks') }}
      </label>
      <p v-if="tasks.length === 0" class="ext:text-sm ext:text-role-on-surface-variant">
        {{ $gettext('You have no tasks you start yourself.') }}
      </p>
      <oc-select
        v-else-if="preset === 'task'"
        :model-value="task"
        :label="$gettext('Task')"
        :options="tasks"
        option-label="name"
        :clearable="false"
        :position-fixed="true"
        data-testid="synaplan-watch-task"
        @update:model-value="task = $event"
      />
    </template>

    <p v-if="phase === 'watching'" class="ext:text-sm" data-testid="synaplan-watch-watching">
      {{ $gettext('Synaplan is watching this.') }}
    </p>

    <p
      v-if="uploadsEnabled === false && (phase === 'setup' || phase === 'watching')"
      class="ext:text-sm ext:text-role-on-surface-variant"
      data-testid="synaplan-watch-no-uploads"
    >
      {{
        $gettext(
          'Files are only sent when you check now. To have them sent while you are away, allow Synaplan to work for you on the Synaplan page.'
        )
      }}
    </p>

    <p v-if="checked" class="ext:text-sm" data-testid="synaplan-watch-checked">
      {{ checkedSentence }}
    </p>

    <p v-if="phase === 'stopped'" class="ext:text-sm" data-testid="synaplan-watch-stopped">
      {{ $gettext('Synaplan no longer watches this.') }}
    </p>

    <div
      v-if="error"
      class="ext:rounded ext:border ext:border-role-error ext:bg-role-error-container ext:p-3 ext:text-sm ext:text-role-on-error-container"
      data-testid="synaplan-watch-error"
    >
      {{ error }}
    </div>

    <div class="ext:flex ext:justify-end ext:gap-2 ext:pt-2">
      <template v-if="phase === 'watching'">
        <oc-button
          appearance="outline"
          :disabled="busy"
          data-testid="synaplan-watch-stop"
          @click="onStop"
        >
          {{ $gettext('Stop watching') }}
        </oc-button>
        <oc-button
          appearance="filled"
          color-role="primary"
          :disabled="busy"
          :show-spinner="busy"
          data-testid="synaplan-watch-check"
          @click="onCheck"
        >
          {{ $gettext('Check now') }}
        </oc-button>
      </template>
      <oc-button
        v-if="phase === 'setup'"
        appearance="filled"
        color-role="primary"
        :disabled="busy || !ready"
        :show-spinner="busy"
        data-testid="synaplan-watch-submit"
        @click="onStart"
      >
        {{ $gettext('Watch with Synaplan') }}
      </oc-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useClientService, type Modal } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'
import { sentenceFor } from '../failures'

const props = defineProps<{
  modal: Modal
  resource: { id: string; name?: string }
  kind: 'file' | 'folder'
}>()

const { $gettext, $ngettext } = useGettext()
const { httpAuthenticated } = useClientService()

const taskSchema = z.object({ id: z.number(), name: z.string() })
type Task = z.infer<typeof taskSchema>

const optionsSchema = z.object({
  tasks: z.array(taskSchema),
  folders: z.array(z.string()),
  watching: z.boolean(),
  uploadsEnabled: z.boolean()
})
const startSchema = z.object({ id: z.number(), uploadsEnabled: z.boolean() })
const checkSchema = z.object({ watches: z.number(), sent: z.number(), failed: z.number() })

type Phase = 'loading' | 'unavailable' | 'setup' | 'watching' | 'stopped'

const phase = ref<Phase>('loading')
const preset = ref<'keep_searchable' | 'task'>('keep_searchable')
const groupKey = ref('')
const task = ref<Task | null>(null)
const tasks = ref<Task[]>([])
const folderOptions = ref<string[]>([])
const uploadsEnabled = ref<boolean | null>(null)
const busy = ref(false)
const error = ref('')
const checked = ref<z.infer<typeof checkSchema> | null>(null)

const target = computed(() => encodeURIComponent(props.resource.id))
const unreachable = () =>
  $gettext('Synaplan could not finish this. Try again in a minute, or ask your administrator.')

const ready = computed(() =>
  preset.value === 'task' ? task.value !== null : groupKey.value.trim() !== ''
)

const checkedSentence = computed(() => {
  const result = checked.value
  if (!result) return ''
  if (result.failed > 0) {
    return $gettext('Checked. Some files could not be sent; Synaplan asks for them again later.')
  }
  if (result.sent === 0) return $gettext('Checked. Synaplan has every file already.')
  return $ngettext(
    'Checked. %{n} file was sent to Synaplan.',
    'Checked. %{n} files were sent to Synaplan.',
    result.sent,
    { n: String(result.sent) }
  )
})

// Knowledge folder names are upper case, as in the knowledge dialog.
function createOption(text: string): string {
  return text.trim().toUpperCase()
}

function onFolderCreated(option: string) {
  if (!folderOptions.value.includes(option)) folderOptions.value.push(option)
  groupKey.value = option
}

onMounted(async () => {
  try {
    const { data } = await httpAuthenticated.get(
      `/api/synaplan/watches/options?targetId=${target.value}`,
      { schema: optionsSchema }
    )
    tasks.value = data.tasks
    folderOptions.value = data.folders
    uploadsEnabled.value = data.uploadsEnabled
    phase.value = data.watching ? 'watching' : 'setup'
  } catch (e) {
    console.error('synaplan watch options failed', e)
    error.value = sentenceFor($gettext, e, unreachable())
    phase.value = 'unavailable'
  }
})

async function onStart() {
  if (!ready.value) return
  busy.value = true
  error.value = ''
  try {
    const { data } = await httpAuthenticated.post(
      '/api/synaplan/watches',
      {
        targetKind: props.kind,
        targetId: props.resource.id,
        preset: preset.value,
        groupKey: preset.value === 'keep_searchable' ? groupKey.value.trim() : '',
        taskId: preset.value === 'task' ? (task.value?.id ?? 0) : 0
      },
      { schema: startSchema }
    )
    uploadsEnabled.value = data.uploadsEnabled
    phase.value = 'watching'
  } catch (e) {
    console.error('synaplan watch start failed', e)
    error.value = sentenceFor($gettext, e, unreachable())
  } finally {
    busy.value = false
  }
}

async function onCheck() {
  busy.value = true
  error.value = ''
  checked.value = null
  try {
    const { data } = await httpAuthenticated.post(
      `/api/synaplan/watches/reconcile?targetId=${target.value}`,
      {},
      { schema: checkSchema, timeout: 0 }
    )
    checked.value = data
  } catch (e) {
    console.error('synaplan watch check failed', e)
    error.value = sentenceFor($gettext, e, unreachable())
  } finally {
    busy.value = false
  }
}

async function onStop() {
  busy.value = true
  error.value = ''
  checked.value = null
  try {
    await httpAuthenticated.delete(`/api/synaplan/watches?targetId=${target.value}`)
    phase.value = 'stopped'
  } catch (e) {
    console.error('synaplan watch stop failed', e)
    error.value = sentenceFor($gettext, e, unreachable())
  } finally {
    busy.value = false
  }
}
</script>
