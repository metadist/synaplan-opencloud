import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: 'html',
  use: {
    baseURL: process.env.BASE_URL_OC ?? 'https://host.docker.internal:9200',
    trace: 'on-first-retry'
  },
  projects: [
    {
      name: 'synaplan-chromium',
      testDir: './tests/e2e',
      use: { ...devices['Desktop Chrome'], browserName: 'chromium', ignoreHTTPSErrors: true }
    }
  ]
})
