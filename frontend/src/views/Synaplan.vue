<template>
  <div
    class="ext:flex ext:h-full ext:items-start ext:justify-center ext:overflow-auto ext:p-8 ext:pt-24"
  >
    <div class="ext:w-full ext:max-w-xl ext:space-y-6">
      <h1 class="ext:text-2xl ext:font-bold" data-testid="synaplan-title">
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
        Test the connection to your Synaplan instance via OIDC token exchange.
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
        v-if="result"
        class="ext:rounded ext:border ext:p-4 ext:text-xs ext:font-mono ext:whitespace-pre-wrap ext:break-all ext:max-h-96 ext:overflow-auto ext:bg-role-surface-container"
        data-testid="synaplan-result"
      >
        {{ JSON.stringify(result, null, 2) }}
      </div>

      <p v-if="error" class="ext:text-sm ext:text-role-error" data-testid="synaplan-error">
        {{ error }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useClientService } from '@opencloud-eu/web-pkg'
import { z } from 'zod'

defineProps<{
  synaplanUrl?: string
}>()

const healthSchema = z.object({
  status: z.string(),
  timestamp: z.string().optional(),
  synaplan_url: z.string().optional(),
  user_id: z.string().optional(),
  token_ok: z.boolean().optional(),
  synaplan_response: z.string().optional(),
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
