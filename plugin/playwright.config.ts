import type { PluginOptions } from '@grafana/plugin-e2e';
import { defineConfig, devices } from '@playwright/test';
import { dirname } from 'node:path';

const pluginE2eAuth = `${dirname(require.resolve('@grafana/plugin-e2e'))}/auth`;

// End-to-end tests run against a real Grafana with the plugin installed
// (`npm run server`, or the CI job), with the engine's API mocked per test.
// GRAFANA_URL points them at another Grafana, e.g. on a port other than 3000.
export default defineConfig<PluginOptions>({
  testDir: './tests',
  fullyParallel: true,
  // A stray test.only must not shrink the CI run to one test.
  forbidOnly: !!process.env.CI,
  // CI retries twice, keeping a trace of the first retry for a flaky failure.
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? 'line' : 'html',
  use: {
    baseURL: process.env.GRAFANA_URL || 'http://localhost:3000',
    trace: 'on-first-retry',
  },
  projects: [
    // Log in once and keep the session for the tests.
    {
      name: 'auth',
      testDir: pluginE2eAuth,
      testMatch: [/.*\.js/],
    },
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        storageState: 'playwright/.auth/admin.json',
      },
      dependencies: ['auth'],
    },
  ],
});
