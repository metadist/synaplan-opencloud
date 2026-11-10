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
      @click="run(signIn, allowFailed)"
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
      @click="run(connect, allowFailed)"
    >
      {{ $gettext('Allow') }}
    </oc-button>
    <oc-button
      v-else
      appearance="outline"
      :disabled="busy"
      :show-spinner="busy"
      data-testid="synaplan-delegation-disconnect"
      @click="run(disconnect, stopFailed)"
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
import {
  PopupBlockedError,
  SignInClosedError,
  SignInDeclinedError,
  SignInRefusedError,
  useDelegation
} from '../composables/useDelegation'

const { $gettext } = useGettext()
const { status, busy, needsSignIn, load, connect, signIn, disconnect } = useDelegation()
const error = ref('')

const allowFailed = () => $gettext('Synaplan could not be allowed to work for you. Try again.')
const stopFailed = () =>
  $gettext('Synaplan could not confirm that it stopped working for you. Try again.')

function messageFor(e: unknown, failed: () => string): string {
  if (e instanceof PopupBlockedError) {
    return $gettext(
      'Your browser blocked the sign-in window. Allow pop-ups for this page and try again.'
    )
  }
  if (e instanceof SignInRefusedError) {
    return $gettext(
      'The sign-in service does not let Synaplan work for you while you are away. Ask your administrator.'
    )
  }
  if (e instanceof SignInDeclinedError) {
    return $gettext('Synaplan was not allowed. You can allow it at any time.')
  }
  if (e instanceof SignInClosedError) {
    return $gettext(
      'The sign-in window closed before Synaplan was allowed. If it showed an error, ask your administrator.'
    )
  }
  return failed()
}

async function run(action: () => Promise<unknown>, failed: () => string) {
  error.value = ''
  try {
    await action()
  } catch (e) {
    error.value = messageFor(e, failed)
  }
}

onMounted(() => run(load, allowFailed))
</script>
