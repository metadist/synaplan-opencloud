<template>
  <div class="ext:flex ext:flex-col ext:gap-3" data-testid="synaplan-from-dialog">
    <p class="ext:text-sm ext:text-role-on-surface-variant">
      {{
        $gettext(
          'Choose a file from Synaplan. It is copied into this folder and stays in Synaplan.'
        )
      }}
    </p>
    <p v-if="loading" class="ext:text-sm">{{ $gettext('Loading…') }}</p>
    <ul v-else class="ext:max-h-64 ext:overflow-auto ext:text-sm" data-testid="synaplan-from-list">
      <li v-for="file in files" :key="file.id">
        <button
          type="button"
          class="ext:w-full ext:px-2 ext:py-1 ext:text-left hover:ext:underline"
          :data-testid="`synaplan-from-file-${file.id}`"
          @click="chosen = file"
        >
          {{ file.name }}
        </button>
      </li>
    </ul>
    <p v-if="files.length === 0 && !loading" class="ext:text-sm">
      {{ $gettext('You have no files in Synaplan yet.') }}
    </p>
    <p v-if="saved" data-testid="synaplan-from-saved">{{ saved }}</p>
    <p v-if="error" class="ext:text-sm ext:text-role-error">{{ error }}</p>
    <div class="ext:flex ext:justify-end">
      <oc-button
        appearance="filled"
        color-role="primary"
        :disabled="!chosen || saving"
        :show-spinner="saving"
        data-testid="synaplan-from-submit"
        @click="onCopy"
      >
        {{ $gettext('Copy into this folder') }}
      </oc-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useClientService, type Modal } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'
import { sentenceFor } from '../failures'

const props = defineProps<{
  modal: Modal
  resource: { id: string; name?: string }
}>()

const { $gettext } = useGettext()
const { httpAuthenticated } = useClientService()
const files = ref<{ id: number; name: string }[]>([])
const chosen = ref<{ id: number; name: string } | null>(null)
const loading = ref(true)
const saving = ref(false)
const saved = ref('')
const error = ref('')

const listSchema = z.object({
  files: z
    .array(
      z.object({ id: z.number(), filename: z.string().optional(), name: z.string().optional() })
    )
    .optional()
    .default([]),
  data: z
    .array(
      z.object({ id: z.number(), filename: z.string().optional(), name: z.string().optional() })
    )
    .optional()
})

onMounted(async () => {
  try {
    const res = await httpAuthenticated.get('/api/synaplan/from-synaplan', { schema: listSchema })
    const rows = res.data.files.length ? res.data.files : (res.data.data ?? [])
    files.value = rows.map((row: { id: number; name?: string; filename?: string }) => ({
      id: row.id,
      name: row.name || row.filename || String(row.id)
    }))
  } catch (e) {
    error.value = sentenceFor(
      e,
      $gettext('Synaplan cannot be reached. Try again in a minute, or ask your administrator.')
    )
  } finally {
    loading.value = false
  }
})

async function onCopy() {
  if (!chosen.value) return
  saving.value = true
  error.value = ''
  try {
    const res = await httpAuthenticated.post(
      '/api/synaplan/from-synaplan',
      { fileId: chosen.value.id, folderId: props.resource.id, name: chosen.value.name },
      { schema: z.object({ sentence: z.string(), path: z.string() }) }
    )
    saved.value = res.data.sentence
  } catch (e) {
    error.value = sentenceFor(e, $gettext('The file was not copied. It is still in Synaplan.'))
  } finally {
    saving.value = false
  }
}
</script>
