<template>
  <!--
    Temporary: a plain button to allow work while the person is away. It is
    here so the person-token flow can be shown and tested end to end. It
    goes away once the actions that need it (watch, keep up to date) ask for
    the permission themselves.
  -->
  <section v-if="status?.available" class="ext:space-y-3" data-testid="synaplan-background">
    <h2 class="ext:text-lg ext:font-semibold">{{ $gettext('While you are away') }}</h2>
    <p class="ext:text-sm" data-testid="synaplan-background-state">
      {{
        status.connected
          ? $gettext('Synaplan may work for you while you are away.')
          : $gettext('Synaplan works for you only while you use OpenCloud.')
      }}
    </p>
    <oc-button
      v-if="!status.connected"
      appearance="filled"
      color-role="primary"
      :disabled="busy"
      :show-spinner="busy"
      data-testid="synaplan-background-connect"
      @click="run(connect)"
    >
      {{ $gettext('Allow') }}
    </oc-button>
    <oc-button
      v-else
      appearance="outline"
      :disabled="busy"
      :show-spinner="busy"
      data-testid="synaplan-background-disconnect"
      @click="run(disconnect)"
    >
      {{ $gettext('Stop') }}
    </oc-button>
    <p v-if="error" class="ext:text-sm ext:text-role-error" data-testid="synaplan-background-error">
      {{ error }}
    </p>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useGettext } from 'vue3-gettext'
import { useBackgroundAccess } from '../composables/useBackgroundAccess'
import { sentenceFor } from '../failures'

const { $gettext } = useGettext()
const { status, busy, load, connect, disconnect, resumeRedirect } = useBackgroundAccess()
const error = ref('')

async function run(action: () => Promise<unknown>) {
  error.value = ''
  try {
    await action()
  } catch (e) {
    error.value = sentenceFor(
      $gettext,
      e,
      $gettext('Synaplan could not be allowed to work for you. Try again.')
    )
  }
}

onMounted(() => run(async () => (await resumeRedirect()) || load()))
</script>
