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

    <div v-if="phase === 'select' || phase === 'error'">
      <oc-select
        :model-value="selectedGroup"
        :label="$gettext('Knowledge group')"
        :options="groupOptions"
        :taggable="true"
        :clearable="false"
        :loading="groupsLoading"
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
        {{ $gettext('Added to the Synaplan knowledge base.') }}
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
        v-if="phase !== 'done'"
        appearance="filled"
        color-role="primary"
        :disabled="phase === 'loading' || !selectedGroup.trim()"
        :show-spinner="phase === 'loading'"
        data-testid="synaplan-knowledge-submit"
        @click="onSubmit"
      >
        {{ phase === 'loading' ? $gettext('Uploading…') : $gettext('Add to Knowledge') }}
      </oc-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
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
  extractedTextLength: z.number()
})

type KnowledgeResult = z.infer<typeof knowledgeResponseSchema>

type Phase = 'select' | 'loading' | 'done' | 'error'

const phase = ref<Phase>('select')
const selectedGroup = ref<string>('')
const result = ref<KnowledgeResult | null>(null)
const error = ref('')

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

  const controller = new AbortController()
  inFlight = controller

  try {
    const data = await loadingService.addTask(async () => {
      const res = await httpAuthenticated.post(
        '/api/synaplan/knowledge',
        {
          resourceId: props.resource.id,
          groupKey: trimmed
        },
        {
          schema: knowledgeResponseSchema,
          signal: controller.signal,
          timeout: 0
        }
      )
      return res.data
    })
    result.value = data
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

function cancel() {
  inFlight?.abort()
}

onBeforeUnmount(() => {
  inFlight?.abort()
})

defineExpose({ onCancel: cancel })
</script>
