import { defineConfig } from '@opencloud-eu/extension-sdk'

export default defineConfig({
  name: 'synaplan',
  test: {
    exclude: ['**/e2e/**']
  }
})
