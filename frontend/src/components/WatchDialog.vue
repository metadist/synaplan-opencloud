<template>
  <div class="ext:flex ext:flex-col ext:gap-3" data-testid="synaplan-watch-dialog">
    <p class="ext:text-sm">
      {{
        kind === 'file'
          ? $gettext('What should happen when this file changes?')
          : $gettext('What should happen with new files?')
      }}
    </p>

    <label class="ext:flex ext:items-center ext:gap-2 ext:text-sm">
      <input v-model="preset" type="radio" value="keep_searchable" />
      {{ kind === 'file' ? $gettext('Keep it searchable') : $gettext('Keep them searchable') }}
    </label>
    <oc-select
      v-if="preset === 'keep_searchable'"
      :model-value="groupKey"
      :label="$gettext('Knowledge folder')"
      :options="folderOptions"
      :taggable="true"
      :clearable="false"
      data-testid="synaplan-watch-folder"
      @update:model-value="groupKey = $event ?? ''"
    />

    <label class="ext:flex ext:items-center ext:gap-2 ext:text-sm">
      <input v-model="preset" type="radio" value="task" :disabled="tasks.length === 0" />
      {{ $gettext('Use one of my tasks') }}
    </label>
    <p v-if="tasks.length === 0" class="ext:text-sm ext:text-role-on-surface-variant">
      {{ $gettext('You have no tasks you start yourself.') }}
    </p>
    <oc-select
      v-else-if="preset === 'task'"
      :model-value="task"
      :label="$gettext('Use one of my tasks')"
      :options="tasks"
      option-label="name"
      :clearable="false"
      data-testid="synaplan-watch-task"
      @update:model-value="task = $event"
    />

    <p class="ext:text-sm ext:text-role-on-surface-variant">{{ consequence }}</p>
    <p v-if="started" data-testid="synaplan-watch-started">
      {{ $gettext('Synaplan is watching this.') }}
    </p>
    <p
      v-if="started && uploadsEnabled === false"
      class="ext:text-sm ext:text-role-error"
      data-testid="synaplan-watch-no-uploads"
    >
      {{
        $gettext(
          'Nothing will be sent yet: this login is not connected to Synaplan. Connect it on the Synaplan page.'
        )
      }}
    </p>
    <p v-if="error" class="ext:text-sm ext:text-role-error" data-testid="synaplan-watch-error">
      {{ error }}
    </p>

    <div class="ext:flex ext:justify-end">
      <oc-button
        appearance="filled"
        color-role="primary"
        :disabled="saving || started"
        :show-spinner="saving"
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
import { invalidateCloudStatus } from '../composables/cloudStatus'

const props = defineProps<{
  modal: Modal
  resource: { id: string; name?: string }
  kind: 'file' | 'folder'
}>()

const { $gettext } = useGettext()
const { httpAuthenticated } = useClientService()
const preset = ref<'keep_searchable' | 'task'>('keep_searchable')
const groupKey = ref('')
const task = ref<{ id: number; name: string } | null>(null)
const tasks = ref<{ id: number; name: string }[]>([])
const folderOptions = ref<string[]>([])
const saving = ref(false)
const started = ref(false)
const uploadsEnabled = ref<boolean | null>(null)
const error = ref('')

const consequence = computed(() =>
  preset.value === 'task'
    ? $gettext(
        'Synaplan runs that task when something new arrives. Stop it any time from the Synaplan tab of this item.'
      )
    : $gettext(
        'New files become searchable in the knowledge folder you chose. Stop it any time from the Synaplan tab of this item.'
      )
)

// Synaplan's cloud watch options: knowledge folders carry group_key.
const folderSchema = z.object({ group_key: z.string(), file_count: z.number().optional() })
const optionsSchema = z.object({
  tasks: z.array(z.object({ id: z.number(), name: z.string() })).default([]),
  folders: z.array(folderSchema).default([]),
  unavailable: z.boolean().optional()
})

onMounted(async () => {
  try {
    const res = await httpAuthenticated.get('/api/synaplan/watches/options', {
      schema: optionsSchema
    })
    if (res.data.unavailable) {
      error.value = $gettext(
        'Synaplan cannot be reached. Try again in a minute, or ask your administrator.'
      )
      return
    }
    tasks.value = res.data.tasks
    folderOptions.value = res.data.folders.map(
      (folder: z.infer<typeof folderSchema>) => folder.group_key
    )
  } catch (e) {
    error.value = sentenceFor(
      $gettext,
      e,
      $gettext('Synaplan cannot be reached. Try again in a minute, or ask your administrator.')
    )
  }
})

async function onStart() {
  saving.value = true
  error.value = ''
  try {
    const res = await httpAuthenticated.post(
      '/api/synaplan/watches',
      {
        targetKind: props.kind,
        targetId: props.resource.id,
        preset: preset.value,
        groupKey: groupKey.value,
        taskId: task.value?.id ?? 0
      },
      { schema: z.object({ uploads_enabled: z.boolean().optional() }).passthrough() }
    )
    uploadsEnabled.value = res.data.uploads_enabled ?? null
    invalidateCloudStatus(props.resource.id)
    started.value = true
  } catch (e) {
    error.value = sentenceFor(
      $gettext,
      e,
      $gettext('Synaplan could not finish this. Try again in a minute, or ask your administrator.')
    )
  } finally {
    saving.value = false
  }
}
</script>
