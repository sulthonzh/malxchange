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
  // The journeys mutate one shared stack (role grants, channels, the
  // audit trail) and later files build on the credentials the first
  // file provisions, so one worker keeps them strictly ordered.
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_ADMIN_WEB_URL ?? 'https://admin.exchange.example.com',
    ignoreHTTPSErrors: true,
    // The edge routes by SNI/Host, so the browser must speak the real
    // hostnames while reaching the published 443 — mapped inside
    // Chromium (one comma-joined rule list), no /etc/hosts edit (the
    // same mechanism apps/web carries). The member host joins for the
    // member-side content journey (#63) and the channel journey's
    // member leg (#62). E2E_ADMIN_WEB_URL overrides bypass it.
    launchOptions: process.env.E2E_ADMIN_WEB_URL
      ? {}
      : {
          args: [
            '--host-resolver-rules=MAP admin.exchange.example.com 127.0.0.1, MAP exchange.example.com 127.0.0.1',
          ],
        },
    trace: 'retain-on-failure',
  },
})
