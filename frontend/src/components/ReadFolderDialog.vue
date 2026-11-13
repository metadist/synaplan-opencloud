<template>
  <div class="ext:flex ext:flex-col ext:gap-4" data-testid="synaplan-read-dialog">
    <p class="ext:text-sm ext:text-role-on-surface-variant">
      {{ resource.name }}
    </p>

    <p
      v-if="phase === 'loading'"
      class="ext:text-sm ext:text-role-on-surface-variant"
      data-testid="synaplan-read-loading"
    >
      {{ $gettext('Loading…') }}
    </p>

    <template v-if="scope">
      <p v-if="scope.named" class="ext:text-sm" data-testid="synaplan-read-allowed">
        {{ $gettext('Synaplan may read the files in this folder when one of your tasks asks.') }}
      </p>
      <p v-else-if="scope.allowed" class="ext:text-sm" data-testid="synaplan-read-inherited">
        {{
          scope.mode === 'all'
            ? $gettext('Synaplan may read all your folders, this one included.')
            : $gettext('Synaplan may read this folder because you allowed a folder it is in.')
        }}
      </p>
      <p v-else class="ext:text-sm" data-testid="synaplan-read-not-allowed">
        {{
          $gettext(
            'Synaplan does not read this folder unless you watch it. Allow it, and your tasks in Synaplan can use the files in it.'
          )
        }}
      </p>
      <p
        v-if="!scope.readsWhileAway"
        class="ext:text-sm ext:text-role-on-surface-variant"
        data-testid="synaplan-read-not-away"
      >
        {{
          $gettext(
            'Synaplan reads folders while you are away. Allow Synaplan to work for you on the Synaplan page first.'
          )
        }}
      </p>
    </template>

    <div
      v-if="error"
      class="ext:rounded ext:border ext:border-role-error ext:bg-role-error-container ext:p-3 ext:text-sm ext:text-role-on-error-container"
      data-testid="synaplan-read-error"
    >
      {{ error }}
    </div>

    <div class="ext:flex ext:justify-end ext:gap-2 ext:pt-2">
      <oc-button
        v-if="scope?.named"
        appearance="outline"
        :disabled="busy"
        :show-spinner="busy"
        data-testid="synaplan-read-stop"
        @click="onChange(false)"
      >
        {{ $gettext('Stop reading this folder') }}
      </oc-button>
      <oc-button
        v-else-if="scope && !scope.allowed"
        appearance="filled"
        color-role="primary"
        :disabled="busy"
        :show-spinner="busy"
        data-testid="synaplan-read-allow"
        @click="onChange(true)"
      >
        {{ $gettext('Allow Synaplan to read this folder') }}
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

const scopeSchema = z.object({
  mode: z.enum(['watched', 'allowed', 'all']),
  named: z.boolean(),
  allowed: z.boolean(),
  readsWhileAway: z.boolean()
})

const phase = ref<'loading' | 'ready' | 'unavailable'>('loading')
const scope = ref<z.infer<typeof scopeSchema> | null>(null)
const busy = ref(false)
const error = ref('')

const unreachable = () =>
  $gettext('Synaplan could not finish this. Try again in a minute, or ask your administrator.')

onMounted(async () => {
  try {
    const { data } = await httpAuthenticated.get(
      `/api/synaplan/read-scope?folderId=${encodeURIComponent(props.resource.id)}`,
      { schema: scopeSchema }
    )
    scope.value = data
    phase.value = 'ready'
  } catch (e) {
    console.error('synaplan read scope failed', e)
    error.value = sentenceFor($gettext, e, unreachable())
    phase.value = 'unavailable'
  }
})

async function onChange(allow: boolean) {
  busy.value = true
  error.value = ''
  try {
    const { data } = await httpAuthenticated.put(
      '/api/synaplan/read-scope',
      { folderId: props.resource.id, allow },
      { schema: scopeSchema }
    )
    scope.value = data
  } catch (e) {
    console.error('synaplan read scope change failed', e)
    error.value = sentenceFor($gettext, e, unreachable())
  } finally {
    busy.value = false
  }
}
</script>
