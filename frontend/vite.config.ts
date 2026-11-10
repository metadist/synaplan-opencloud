import { defineConfig } from '@opencloud-eu/extension-sdk'

export default defineConfig({
  name: 'synaplan',
  server: {
    port: 9210,
    strictPort: true
  },
  test: {
    exclude: ['**/e2e/**']
  }
})
