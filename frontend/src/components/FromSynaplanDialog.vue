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
    <p v-if="loading && files.length === 0" class="ext:text-sm">{{ $gettext('Loading…') }}</p>
    <ul v-else class="ext:max-h-64 ext:overflow-auto ext:text-sm" data-testid="synaplan-from-list">
      <li v-for="file in shown" :key="file.id">
        <button
          type="button"
          class="ext:w-full ext:px-2 ext:py-1 ext:text-left hover:ext:underline"
          :class="{ 'ext:font-semibold': chosen?.id === file.id }"
          :aria-pressed="chosen?.id === file.id"
          :disabled="saving"
          :data-testid="`synaplan-from-file-${file.id}`"
          @click="chosen = file"
        >
          {{ file.name }}
        </button>
      </li>
    </ul>
    <p v-if="files.length === 0 && !loading && !error" class="ext:text-sm">
      {{ $gettext('You have no files in Synaplan yet.') }}
    </p>
    <oc-button
      v-if="hasMore && !loading"
      appearance="outline"
      :disabled="saving"
      :show-spinner="loadingMore"
      data-testid="synaplan-from-more"
      @click="loadOlder"
    >
      {{ $gettext('Show older files') }}
    </oc-button>
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
  display_name: z.string().nullish(),
  original_name: z.string().nullish(),
  filename: z.string().nullish()
})
const listSchema = z.object({
  files: z.array(fileRow).optional().default([]),
  pagination: z.object({ page: z.number(), pages: z.number() }).optional()
})

const filter = ref('')
const hasMore = ref(false)
const loadingMore = ref(false)
const nextPage = ref(2)
const shown = computed(() => {
  const needle = filter.value.trim().toLowerCase()
  if (!needle) return files.value
  return files.value.filter((file) => file.name.toLowerCase().includes(needle))
})

function remember(rows: z.infer<typeof fileRow>[]) {
  const seen = new Set(files.value.map((file) => file.id))
  for (const row of rows) {
    if (seen.has(row.id)) continue
    files.value.push({
      id: row.id,
      name: row.display_name || row.original_name || row.filename || String(row.id)
    })
  }
}

async function loadPage(page: number) {
  const res = await httpAuthenticated.get(`/api/synaplan/from-synaplan?page=${page}`, {
    schema: listSchema
  })
  remember(res.data.files)
  const pages = res.data.pagination?.pages ?? 1
  nextPage.value = page + 1
  hasMore.value = nextPage.value <= pages
}

onMounted(async () => {
  try {
    await loadPage(1)
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

async function loadOlder() {
  if (!hasMore.value || loadingMore.value || saving.value) return
  loadingMore.value = true
  error.value = ''
  try {
    await loadPage(nextPage.value)
  } catch (e) {
    error.value = sentenceFor(
      $gettext,
      e,
      $gettext('Synaplan cannot be reached. Try again in a minute, or ask your administrator.')
    )
  } finally {
    loadingMore.value = false
  }
}

async function onCopy() {
  const file = chosen.value
  if (!file || saving.value) return
  saving.value = true
  error.value = ''
  saved.value = ''
  try {
    const res = await httpAuthenticated.post(
      '/api/synaplan/from-synaplan',
      { fileId: file.id, folderId: props.resource.id, name: file.name },
      { schema: z.object({ path: z.string() }) }
    )
    const written = res.data.path.split('/').filter(Boolean).pop() || file.name
    // The service can answer in English. The sentence the person reads
    // comes from the catalog, the same way every other action does.
    saved.value = $gettext('Copied %{name} into your files. The file is still in Synaplan.', {
      name: written
    })
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
