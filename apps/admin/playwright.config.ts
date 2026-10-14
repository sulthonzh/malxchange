// Playwright admin journey (spec #25, issue #56): the Employee path
// first-login forced setup (password rotation → TOTP enrollment) → the
// permission-derived shell, plus a grants-free Employee whose sidebar
// drops the routes and whose api calls the server refuses. Run with
// the compose test+e2e profiles up (`make admin-e2e`).
//
// The browser drives the app THROUGH the Caddy edge
// (https://admin.exchange.example.com, internal CA — ignoreHTTPSErrors)
// so the REST surface is same-origin exactly as in production.
import { defineConfig } from '@playwright/test'
export default defineConfig({
  testDir: './e2e',
  timeout: 120 * 1000,
  retries: process.env.CI ? 1 : 0,
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_ADMIN_WEB_URL ?? 'https://admin.exchange.example.com',
    ignoreHTTPSErrors: true,
    // The edge routes by SNI/Host, so the browser must speak the real
    // hostname while reaching the published 443 — mapped inside
    // Chromium, no /etc/hosts edit (the same rule apps/web carries).
    // E2E_ADMIN_WEB_URL overrides bypass it (another origin).
    launchOptions: process.env.E2E_ADMIN_WEB_URL
      ? {}
      : { args: ['--host-resolver-rules=MAP admin.exchange.example.com 127.0.0.1'] },
    trace: 'retain-on-failure',
  },
})
