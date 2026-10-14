// Runtime host configuration for the admin app (spec #25, issue #56):
// every host this app talks to arrives through runtimeConfig, fed by
// environment variables (NUXT_ADMIN_API_BASE_URL server-side,
// NUXT_PUBLIC_ADMIN_API_BASE_URL in the browser — see .env.example).
// The server override exists because SSR runs inside the container
// network (http://admin-api:8080) while the browser speaks the public
// admin origin through the Caddy edge. Both bases are origins — the
// admin-api serves its surface under /api/* itself. Nothing is baked
// into source; defaults stay empty so an unconfigured app fails loudly
// at the api client instead of silently calling some baked-in host.
//
// History-mode routes only (the legacy admin's hash-mode router dies
// with ADR-0003's Nuxt port); the app is REST-only — no WebSocket.
export default defineNuxtConfig({
  srcDir: 'src',
  ssr: true,
  modules: ['@element-plus/nuxt'],
  css: ['~/assets/scss/main.scss'],
  runtimeConfig: {
    // SSR origin (NUXT_ADMIN_API_BASE_URL): the in-network compose
    // address (http://admin-api:8080). Falls back to the public base
    // when unset.
    adminApiBaseUrl: '',
    public: {
      adminApiBaseUrl: '',
    },
  },
  app: {
    head: {
      htmlAttrs: { lang: 'en' }, // English-only UI (ADR-0013)
      meta: [{ name: 'viewport', content: 'width=device-width, initial-scale=1' }],
    },
  },
})
