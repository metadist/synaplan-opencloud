<template>
  <section v-if="status?.available" class="ext:space-y-3" data-testid="synaplan-delegation">
    <h2 class="ext:text-lg ext:font-semibold">{{ $gettext('While you are away') }}</h2>
    <p class="ext:text-sm" data-testid="synaplan-delegation-state">
      {{
        status.connected
          ? $gettext('Synaplan may work for you while you are away.')
          : $gettext('Synaplan works for you only while you use OpenCloud.')
      }}
    </p>
    <p v-if="!status.connected && needsSignIn" class="ext:text-sm">
      {{ $gettext('The sign-in service needs you once. It opens in a new window.') }}
    </p>
    <oc-button
      v-if="!status.connected && needsSignIn"
      appearance="filled"
      color-role="primary"
      :disabled="busy"
      :show-spinner="busy"
      data-testid="synaplan-delegation-sign-in"
      @click="run(signIn)"
    >
      {{ $gettext('Sign in to allow') }}
    </oc-button>
    <oc-button
      v-else-if="!status.connected"
      appearance="filled"
      color-role="primary"
      :disabled="busy"
      :show-spinner="busy"
      data-testid="synaplan-delegation-connect"
      @click="run(connect)"
    >
      {{ $gettext('Allow') }}
    </oc-button>
    <oc-button
      v-else
      appearance="outline"
      :disabled="busy"
      :show-spinner="busy"
      data-testid="synaplan-delegation-disconnect"
      @click="run(disconnect)"
    >
      {{ $gettext('Stop') }}
    </oc-button>
    <p v-if="error" class="ext:text-sm ext:text-role-error" data-testid="synaplan-delegation-error">
      {{ error }}
    </p>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useGettext } from 'vue3-gettext'
import { useDelegation } from '../composables/useDelegation'

const { $gettext } = useGettext()
const { status, busy, needsSignIn, load, connect, signIn, disconnect } = useDelegation()
const error = ref('')

async function run(action: () => Promise<unknown>) {
  error.value = ''
  try {
    await action()
  } catch {
    error.value = $gettext('Synaplan could not be allowed to work for you. Try again.')
  }
}

onMounted(() => run(load))
</script>
