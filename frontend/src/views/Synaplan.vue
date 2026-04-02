<template>
  <div
    class="ext:flex ext:h-full ext:items-start ext:justify-center ext:overflow-auto ext:p-8 ext:pt-24"
  >
    <div class="ext:w-full ext:max-w-xl ext:space-y-6">
      <h1 class="ext:text-2xl ext:font-bold" data-testid="synaplan-title">Synaplan</h1>

      <p class="ext:text-sm ext:text-gray-500">
        Test the connection to your Synaplan instance via OIDC token exchange.
      </p>

      <button
        class="ext:inline-flex ext:items-center ext:justify-center ext:gap-2 ext:rounded ext:bg-blue-600 ext:px-5 ext:py-2 ext:text-sm ext:text-white hover:ext:bg-blue-700 disabled:ext:opacity-50 disabled:ext:cursor-not-allowed"
        data-testid="synaplan-test-btn"
        :disabled="loading"
        @click="testConnection"
      >
        <svg
          class="ext:h-4 ext:w-4"
          :class="loading ? 'ext:animate-spin' : 'ext:invisible'"
          viewBox="0 0 24 24"
          fill="none"
        >
          <circle
            class="ext:opacity-25"
            cx="12"
            cy="12"
            r="10"
            stroke="currentColor"
            stroke-width="4"
          />
          <path
            class="ext:opacity-75"
            fill="currentColor"
            d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
          />
        </svg>
        Test Connection
      </button>

      <div
        v-if="result"
        class="ext:rounded ext:border ext:p-4 ext:text-xs ext:font-mono ext:whitespace-pre-wrap ext:break-all ext:max-h-96 ext:overflow-auto"
        :class="
          result.status === 'ok'
            ? 'ext:bg-green-50 ext:text-green-800 ext:border-green-200'
            : 'ext:bg-red-50 ext:text-red-800 ext:border-red-200'
        "
        data-testid="synaplan-result"
      >
        {{ JSON.stringify(result, null, 2) }}
      </div>

      <p v-if="error" class="ext:text-sm ext:text-red-500" data-testid="synaplan-error">
        {{ error }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useClientService } from '@opencloud-eu/web-pkg'
import { z } from 'zod'

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
