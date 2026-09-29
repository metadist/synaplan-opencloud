<template>
  <div class="ext:flex ext:flex-col ext:gap-4" data-testid="synaplan-knowledge-dialog">
    <img
      :src="birdSrc"
      alt=""
      class="ext:h-10 ext:w-10 ext:self-center"
      data-testid="synaplan-knowledge-logo"
    />

    <p class="ext:text-sm ext:text-role-on-surface-variant">
      {{ resource.name }}
    </p>

    <p v-if="knowledgeStatus.stale" class="ext:text-sm" data-testid="synaplan-knowledge-stale">
      {{
        $gettext('This file changed after it was added. Update it to refresh the knowledge copy.')
      }}
    </p>

    <p v-if="removed" class="ext:text-sm" data-testid="synaplan-knowledge-removed">
      {{ $gettext('Removed from the knowledge base. It is no longer searchable.') }}
    </p>

    <div v-if="phase === 'select' || phase === 'error'">
      <oc-select
        :model-value="selectedGroup"
        :label="$gettext('Knowledge group')"
        :options="groupOptions"
        :taggable="true"
        :clearable="false"
        :loading="groupsLoading"
        :position-fixed="true"
        :create-option="createOption"
        :description-message="
          $gettext('Pick an existing group or type a new name — Synaplan will create it.')
        "
        data-testid="synaplan-knowledge-group"
        @update:model-value="onGroupChange"
        @option:created="onGroupCreated"
      />
    </div>

    <div
      v-if="phase === 'loading'"
      class="ext:flex ext:flex-col ext:items-center ext:gap-2 ext:py-6 ext:text-sm ext:text-role-on-surface-variant"
      data-testid="synaplan-knowledge-loading"
    >
      <p>{{ $gettext('Uploading and vectorizing…') }}</p>
      <p class="ext:text-xs">
        {{ $gettext('This can take a few seconds for large documents.') }}
      </p>
    </div>

    <div
      v-if="phase === 'done' && result"
      class="ext:rounded ext:border ext:bg-role-surface-container ext:p-3 ext:text-sm"
      data-testid="synaplan-knowledge-success"
    >
      <p class="ext:font-semibold ext:mb-2">
        {{
          wasUpdate
            ? $gettext('Updated in the Synaplan knowledge base.')
            : $gettext('Added to the Synaplan knowledge base.')
        }}
      </p>
      <dl class="ext:grid ext:grid-cols-2 ext:gap-x-4 ext:gap-y-1 ext:text-xs">
        <dt class="ext:text-role-on-surface-variant">{{ $gettext('Group') }}</dt>
        <dd>{{ result.groupKey }}</dd>
        <dt class="ext:text-role-on-surface-variant">{{ $gettext('Chunks created') }}</dt>
        <dd>{{ result.chunksCreated }}</dd>
        <template v-if="result.extractedTextLength > 0">
          <dt class="ext:text-role-on-surface-variant">{{ $gettext('Text extracted') }}</dt>
          <dd>{{ result.extractedTextLength }} {{ $gettext('characters') }}</dd>
        </template>
      </dl>
    </div>

    <div
      v-if="phase === 'error'"
      class="ext:rounded ext:border ext:border-role-error ext:bg-role-error-container ext:p-3 ext:text-sm ext:text-role-on-error-container"
      data-testid="synaplan-knowledge-error"
    >
      {{ error }}
    </div>

    <div class="ext:flex ext:justify-end ext:gap-2 ext:pt-2">
      <oc-button
        v-if="knowledgeStatus.inKnowledge && phase !== 'loading'"
        appearance="outline"
        data-testid="synaplan-knowledge-remove"
        @click="onRemove"
      >
        {{ $gettext('Remove from Knowledge') }}
      </oc-button>

      <oc-button
        v-if="phase !== 'done'"
        appearance="filled"
        color-role="primary"
        :disabled="phase === 'loading' || !selectedGroup.trim()"
        :show-spinner="phase === 'loading'"
        data-testid="synaplan-knowledge-submit"
        @click="onSubmit"
      >
        {{ submitLabel }}
      </oc-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useClientService, useLoadingService, type Modal } from '@opencloud-eu/web-pkg'
import { useGettext } from 'vue3-gettext'
import { z } from 'zod'
import { useSynaplanBird } from '../composables/useSynaplanBird'
import { useKnowledgeGroups } from '../composables/useKnowledgeGroups'

const props = defineProps<{
  modal: Modal
  resource: {
    id: string
    name?: string
    mimeType?: string
  }
}>()

const { $gettext } = useGettext()
const { httpAuthenticated } = useClientService()
const loadingService = useLoadingService()
const birdSrc = useSynaplanBird()

const { groups, loading: groupsLoading } = useKnowledgeGroups()

// vue-select can take strings as options. We map the loaded groups
// to a name list and append any tag the user creates inline so it
// remains selectable in the dropdown.
const fetchedGroupNames = computed(() => groups.value.map((g) => g.name))
const localGroupNames = ref<string[]>([])
const groupOptions = computed(() => {
  const merged = [...fetchedGroupNames.value]
  for (const name of localGroupNames.value) {
    if (!merged.includes(name)) merged.push(name)
  }
  return merged
})

const knowledgeResponseSchema = z.object({
  groupKey: z.string(),
  vectorized: z.boolean(),
  chunksCreated: z.number(),
  extractedTextLength: z.number(),
  synaplanFileId: z.number().optional()
})

type KnowledgeResult = z.infer<typeof knowledgeResponseSchema>

type Phase = 'select' | 'loading' | 'done' | 'error'

const phase = ref<Phase>('select')
const selectedGroup = ref<string>('')
const result = ref<KnowledgeResult | null>(null)
const error = ref('')
const removed = ref(false)
const wasUpdate = ref(false)

const statusSchema = z.object({
  inKnowledge: z.boolean(),
  stale: z.boolean(),
  synaplanFileId: z.number()
})

const knowledgeStatus = ref({ inKnowledge: false, stale: false, synaplanFileId: 0 })

const submitLabel = computed(() => {
  if (phase.value === 'loading') {
    return knowledgeStatus.value.inKnowledge ? $gettext('Updating…') : $gettext('Uploading…')
  }
  return knowledgeStatus.value.inKnowledge
    ? $gettext('Update in Knowledge')
    : $gettext('Add to Knowledge')
})

onMounted(async () => {
  try {
    const { data } = await httpAuthenticated.get(
      `/api/synaplan/knowledge/status?resourceId=${encodeURIComponent(props.resource.id)}`,
      { schema: statusSchema }
    )
    knowledgeStatus.value = data
  } catch (e) {
    console.warn('synaplan knowledge status: fetch failed', e)
  }
})

let inFlight: AbortController | null = null

// vue-select calls createOption with the typed search string when
// taggable is true and the user creates a new tag. We normalise to
// uppercase to match the synaplan-nextcloud convention.
function createOption(text: string): string {
  return text.trim().toUpperCase()
}

function onGroupChange(value: string | null) {
  selectedGroup.value = value ?? ''
}

function onGroupCreated(option: string) {
  if (!localGroupNames.value.includes(option)) {
    localGroupNames.value.push(option)
  }
  selectedGroup.value = option
}

async function onSubmit() {
  const trimmed = selectedGroup.value.trim()
  if (!trimmed) return

  phase.value = 'loading'
  error.value = ''
  result.value = null
  wasUpdate.value = knowledgeStatus.value.inKnowledge

  const controller = new AbortController()
  inFlight = controller

  try {
    const data = await loadingService.addTask(async () => {
      const body: { resourceId: string; groupKey: string; overwrite?: boolean } = {
        resourceId: props.resource.id,
        groupKey: trimmed
      }
      if (knowledgeStatus.value.inKnowledge) body.overwrite = true
      const res = await httpAuthenticated.post('/api/synaplan/knowledge', body, {
        schema: knowledgeResponseSchema,
        signal: controller.signal,
        timeout: 0
      })
      return res.data
    })
    result.value = data
    knowledgeStatus.value = {
      inKnowledge: true,
      stale: false,
      synaplanFileId: data.synaplanFileId ?? knowledgeStatus.value.synaplanFileId
    }
    phase.value = 'done'
  } catch (e) {
    if (controller.signal.aborted) return
    console.error('synaplan knowledge upload failed', e)
    error.value = e instanceof Error && e.message ? e.message : $gettext('Knowledge upload failed')
    phase.value = 'error'
  } finally {
    if (inFlight === controller) inFlight = null
  }
}

async function onRemove() {
  const id = knowledgeStatus.value.synaplanFileId
  if (!id) return
  phase.value = 'loading'
  error.value = ''
  try {
    await httpAuthenticated.delete(`/api/synaplan/knowledge/${id}`)
    knowledgeStatus.value = { inKnowledge: false, stale: false, synaplanFileId: 0 }
    removed.value = true
    phase.value = 'select'
  } catch (e) {
    console.error('synaplan knowledge remove failed', e)
    error.value =
      e instanceof Error && e.message ? e.message : $gettext('Could not remove the file')
    phase.value = 'error'
  }
}

function cancel() {
  inFlight?.abort()
}

onBeforeUnmount(() => {
  inFlight?.abort()
})

defineExpose({ onCancel: cancel })
</script>
