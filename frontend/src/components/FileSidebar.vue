<template>
  <div class="ext:flex ext:flex-col ext:gap-3 ext:p-2 ext:text-sm" data-testid="synaplan-sidebar">
    <p v-if="loading">{{ $gettext('Loading…') }}</p>
    <template v-else-if="row">
      <p data-testid="synaplan-sidebar-state">{{ stateSentence }}</p>
      <p v-if="folderName" data-testid="synaplan-sidebar-folder">
        {{ $gettext('Knowledge folder') }}: {{ folderName }}
      </p>
      <oc-button
        v-if="fileId"
        appearance="outline"
        :disabled="removing"
        data-testid="synaplan-sidebar-remove"
        @click="onRemove"
      >
        {{ $gettext('Remove from the knowledge folder') }}
      </oc-button>
    </template>
    <p v-else data-testid="synaplan-sidebar-not-searchable">
      {{ $gettext('This file is not searchable by AI.') }}
    </p>
    <p v-if="message" data-testid="synaplan-sidebar-message">{{ message }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useClientService } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'
import {
  invalidateCloudStatus,
  loadCloudStatus,
  markFor,
  type CloudMark
} from '../composables/cloudStatus'
import { sentenceFor } from '../failures'

const props = defineProps<{
  resource?: { id?: string; etag?: string; name?: string }
}>()

const { $gettext } = useGettext()
const { httpAuthenticated } = useClientService()
const loading = ref(true)
const removing = ref(false)
const message = ref('')

// A Synaplan without cloud file state answers no bird; the knowledge
// status still knows whether this file is searchable.
const fallback = ref<CloudMark | undefined>(undefined)
const row = computed(() => {
  const id = props.resource?.id
  if (!id) return undefined
  const mark = markFor(id) ?? fallback.value
  return mark && mark.state !== 'none' ? mark : undefined
})
const folderName = computed(() => row.value?.folder ?? '')
const fileId = computed(() => row.value?.fileId ?? 0)
const stateSentence = computed(() => {
  switch (row.value?.state) {
    case 'adding':
      return $gettext('Being made searchable.')
    case 'updating':
      return $gettext('Changed, updating.')
    case 'changed':
      return $gettext('Changed since it was made searchable.')
    case 'failed':
      return $gettext('Synaplan could not make this file searchable.')
    default:
      return $gettext('Searchable by AI.')
  }
})

const knowledgeSchema = z.object({
  inKnowledge: z.boolean(),
  stale: z.boolean(),
  synaplanFileId: z.number()
})

async function knowledgeMark(id: string): Promise<CloudMark | undefined> {
  try {
    const res = await httpAuthenticated.get(
      `/api/synaplan/knowledge/status?resourceId=${encodeURIComponent(id)}`,
      { schema: knowledgeSchema }
    )
    if (!res.data.inKnowledge) return undefined
    return {
      state: res.data.stale ? 'changed' : 'searchable',
      folder: '',
      fileId: res.data.synaplanFileId
    }
  } catch {
    return undefined
  }
}

async function refresh() {
  loading.value = true
  message.value = ''
  fallback.value = undefined
  const id = props.resource?.id
  if (id) {
    invalidateCloudStatus(id)
    await loadCloudStatus(httpAuthenticated, [{ id, version: props.resource?.etag ?? '' }])
    if (!markFor(id) || markFor(id)?.state === 'none') fallback.value = await knowledgeMark(id)
  }
  loading.value = false
}

// The panel stays mounted while the person clicks through files.
onMounted(refresh)
watch(() => props.resource?.id, refresh)

async function onRemove() {
  const id = props.resource?.id
  if (!id) return
  removing.value = true
  try {
    await httpAuthenticated.delete(`/api/synaplan/knowledge?resourceId=${encodeURIComponent(id)}`, {
      schema: z.object({ removed: z.boolean() })
    })
    invalidateCloudStatus(id)
    fallback.value = undefined
    message.value = $gettext('Removed from the knowledge folder. It is no longer searchable.')
  } catch (e) {
    console.error('synaplan sidebar remove failed', e)
    message.value = sentenceFor($gettext, e, $gettext('Could not remove the file'))
  } finally {
    removing.value = false
  }
}
</script>
