<template>
  <div class="ext:flex ext:flex-col ext:gap-3" data-testid="synaplan-from-dialog">
    <p class="ext:text-sm ext:text-role-on-surface-variant">
      {{
        $gettext(
          'Choose a file from Synaplan. It is copied into this folder and stays in Synaplan.'
        )
      }}
    </p>
    <oc-text-input
      v-if="files.length > 8"
      v-model="filter"
      :label="$gettext('Find a file')"
      data-testid="synaplan-from-filter"
    />
    <p v-if="loading" class="ext:text-sm">{{ $gettext('Loading…') }}</p>
    <ul v-else class="ext:max-h-64 ext:overflow-auto ext:text-sm" data-testid="synaplan-from-list">
      <li v-for="file in shown" :key="file.id">
        <button
          type="button"
          class="ext:w-full ext:px-2 ext:py-1 ext:text-left hover:ext:underline"
          :class="{ 'ext:font-semibold': chosen?.id === file.id }"
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
    <p v-if="hasMore && !loading" class="ext:text-sm ext:text-role-on-surface-variant">
      {{ $gettext('Only the newest files are listed. Type part of a name to narrow the list.') }}
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
import { computed, onMounted, ref } from 'vue'
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

const fileRow = z.object({
  id: z.number(),
  filename: z.string().optional(),
  name: z.string().optional()
})
const listSchema = z.object({
  files: z.array(fileRow).optional().default([]),
  data: z.array(fileRow).optional(),
  pagination: z.object({ page: z.number(), pages: z.number() }).optional()
})

/** Pages fetched before the dialog stops and asks the person to narrow the list. */
const MAX_PAGES = 10

const filter = ref('')
const hasMore = ref(false)
const shown = computed(() => {
  const needle = filter.value.trim().toLowerCase()
  if (!needle) return files.value
  return files.value.filter((file) => file.name.toLowerCase().includes(needle))
})

onMounted(async () => {
  try {
    const rows: z.infer<typeof fileRow>[] = []
    let page = 1
    let pages = 1
    do {
      const res = await httpAuthenticated.get(`/api/synaplan/from-synaplan?page=${page}`, {
        schema: listSchema
      })
      rows.push(...(res.data.files.length ? res.data.files : (res.data.data ?? [])))
      pages = res.data.pagination?.pages ?? 1
      page += 1
    } while (page <= pages && page <= MAX_PAGES)
    hasMore.value = pages > MAX_PAGES
    files.value = rows.map((row) => ({
      id: row.id,
      name: row.name || row.filename || String(row.id)
    }))
  } catch (e) {
    error.value = sentenceFor(
      $gettext,
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
    error.value = sentenceFor(
      $gettext,
      e,
      $gettext('The file was not copied. It is still in Synaplan.')
    )
  } finally {
    saving.value = false
  }
}
</script>
