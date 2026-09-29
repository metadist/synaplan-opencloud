<template>
  <div
    class="ext:flex ext:h-full ext:items-start ext:justify-center ext:overflow-auto ext:p-8 ext:pt-24"
  >
    <div class="ext:w-full ext:max-w-xl ext:space-y-6">
      <h1
        class="ext:text-2xl ext:font-bold ext:flex ext:items-center ext:gap-3"
        data-testid="synaplan-title"
      >
        <img
          :src="birdSrc"
          alt=""
          class="ext:h-8 ext:w-8 ext:shrink-0"
          data-testid="synaplan-title-logo"
        />
        <a
          v-if="synaplanUrl"
          :href="synaplanUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="hover:ext:underline"
          data-testid="synaplan-title-link"
        >
          Synaplan
        </a>
        <template v-else>Synaplan</template>
      </h1>

      <p class="ext:text-sm ext:text-role-on-surface-variant">
        Your OpenCloud login is your Synaplan account. There is no second password.
      </p>

      <p v-if="modelsConfigUrl" class="ext:text-sm">
        <a
          :href="modelsConfigUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="hover:ext:underline"
          data-testid="synaplan-models-link"
        >
          Manage AI models →
        </a>
      </p>

      <oc-button
        appearance="filled"
        color-role="primary"
        :disabled="loading"
        :show-spinner="loading"
        data-testid="synaplan-test-btn"
        @click="testConnection"
      >
        Test Connection
      </oc-button>

      <div
        v-if="result?.account?.email"
        class="ext:rounded ext:border ext:bg-role-surface-container ext:p-4 ext:text-sm"
        data-testid="synaplan-account"
      >
        {{ $gettext('Signed in to Synaplan as') }} {{ result.account.email }}.
      </div>

      <p v-else-if="result && result.status !== 'ok'" class="ext:text-sm ext:text-role-error">
        {{ $gettext('Synaplan did not accept this OpenCloud login.') }}
      </p>

      <p v-if="error" class="ext:text-sm ext:text-role-error" data-testid="synaplan-error">
        {{ error }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useGettext } from 'vue3-gettext'
import { useClientService } from '@opencloud-eu/web-pkg'
import { z } from 'zod'
import { useSynaplanBird } from '../composables/useSynaplanBird'

const props = defineProps<{
  synaplanUrl?: string
}>()

const birdSrc = useSynaplanBird()

const modelsConfigUrl = computed(() =>
  props.synaplanUrl ? `${props.synaplanUrl.replace(/\/+$/, '')}/config/ai-models` : ''
)

const { $gettext } = useGettext()

const healthSchema = z.object({
  status: z.string(),
  timestamp: z.string().optional(),
  synaplanUrl: z.string().optional(),
  userId: z.string().optional(),
  account: z
    .object({
      email: z.string().optional(),
      firstName: z.string().optional(),
      level: z.string().optional()
    })
    .optional(),
  synaplanResponse: z.string().optional(),
  error: z.string().optional()
})

type HealthResult = z.infer<typeof healthSchema>

const { httpAuthenticated } = useClientService()

const loading = ref(false)
const result = ref<HealthResult | null>(null)
const error = ref('')

async function testConnection() {
  loading.value = true
  error.value = ''

  try {
    const { data } = await httpAuthenticated.get('/api/synaplan/me', { schema: healthSchema })
    result.value = data
  } catch (e) {
    console.error('Connection test failed', e)
    error.value = 'Connection test failed — check console for details'
  } finally {
    loading.value = false
  }
}
</script>
