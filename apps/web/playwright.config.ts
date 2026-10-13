// Playwright system journey (spec #24, issue #51): the member path
// register (code from the test mail sink) → login → KYC submit → admin
// approve → TOTP bind → deposit (simnet chain) → place/cancel orders →
// withdraw (admin audit → simnet broadcast), against the compose test
// profile. Not part of `npm test` (vitest): run `npm run e2e` with the
// stack up.
//
// The browser drives the app THROUGH the Caddy edge (https://127.0.0.1,
// internal CA — ignoreHTTPSErrors) so the api and WebSocket are
// same-origin exactly as in production; the split :3000/:18080 host
// bindings would make every browser fetch cross-origin. API-level specs
// use E2E_API_URL against the api directly.
import { defineConfig } from '@playwright/test'
export default defineConfig({
  testDir: './e2e',
  timeout: 120 * 1000,
  retries: process.env.CI ? 1 : 0,
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_WEB_URL ?? 'https://exchange.example.com',
    ignoreHTTPSErrors: true,
    // The edge routes by SNI/Host, so the browser must speak the real
    // hostname while reaching the published 443 — mapped inside
    // Chromium, no /etc/hosts edit. E2E_WEB_URL overrides bypass it
    // (another origin, e.g. :3000 direct).
    launchOptions: process.env.E2E_WEB_URL
      ? {}
      : { args: ['--host-resolver-rules=MAP exchange.example.com 127.0.0.1, MAP admin.exchange.example.com 127.0.0.1'] },
    trace: 'retain-on-failure',
  },
})
