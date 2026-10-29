<template>
  <div class="ext:flex ext:flex-col ext:gap-3 ext:p-2 ext:text-sm" data-testid="synaplan-sidebar">
    <p v-if="loading">{{ $gettext('Loading…') }}</p>
    <template v-else-if="row">
      <p data-testid="synaplan-sidebar-state">{{ stateSentence }}</p>
      <p v-if="folderName">{{ $gettext('Knowledge folder') }}: {{ folderName }}</p>
      <oc-button
        v-if="fileId"
        appearance="outline"
        data-testid="synaplan-sidebar-remove"
        @click="onRemove"
      >
        {{ $gettext('Remove from the knowledge folder') }}
      </oc-button>
    </template>
    <p v-else>{{ $gettext('This file is not searchable by AI.') }}</p>
    <p v-if="message">{{ message }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useClientService } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'
import { loadCloudStatus, markFor } from '../composables/cloudStatus'

const props = defineProps<{
  resource?: { id?: string; etag?: string; name?: string }
}>()

const { $gettext } = useGettext()
const { httpAuthenticated } = useClientService()
const loading = ref(true)
const message = ref('')

const row = computed(() => (props.resource?.id ? markFor(props.resource.id) : undefined))
const folderName = computed(() => row.value?.folder ?? '')
const fileId = computed(() => row.value?.fileId ?? 0)
const stateSentence = computed(() => {
  switch (row.value?.state) {
    case 'searchable':
      return $gettext('Searchable by AI.')
    case 'updating':
    case 'adding':
      return $gettext('Changed — updating.')
    case 'changed':
      return $gettext('Changed since it was made searchable.')
    case 'failed':
      return $gettext('Synaplan could not make this file searchable.')
    default:
      return $gettext('This file is not searchable by AI.')
  }
})

onMounted(async () => {
  if (props.resource?.id) {
    await loadCloudStatus(httpAuthenticated, [
      { id: props.resource.id, version: props.resource.etag ?? '' }
    ])
  }
  loading.value = false
})

async function onRemove() {
  if (!props.resource?.id) return
  try {
    await httpAuthenticated.delete(
      `/api/synaplan/knowledge?resourceId=${encodeURIComponent(props.resource.id)}`,
      { schema: z.object({ removed: z.boolean() }) }
    )
    message.value = $gettext('Removed from the knowledge folder. It is no longer searchable.')
  } catch {
    message.value = $gettext(
      'Synaplan could not finish this. Try again in a minute, or ask your administrator.'
    )
  }
}
</script>
