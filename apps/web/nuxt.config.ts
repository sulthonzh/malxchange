// Runtime host configuration (spec #24, story 33): every host this app
// talks to arrives through runtimeConfig, fed by environment variables
// (NUXT_API_BASE_URL server-side, NUXT_PUBLIC_API_BASE_URL and
// NUXT_PUBLIC_WS_URL in the browser — see .env.example). The server
// override exists because SSR runs inside the container network: the
// browser's 127.0.0.1 host would be the app itself. Nothing is baked
// into source; the legacy front's hardcoded Vue.prototype.host /
// rootHost literals die here. Defaults stay empty so an unconfigured
// app fails loudly at the api client instead of silently calling some
// baked-in host.
export default defineNuxtConfig({
  srcDir: 'src',
  ssr: true,
  modules: ['@element-plus/nuxt'],
  css: ['~/assets/scss/main.scss'],
  runtimeConfig: {
    // SSR origin (NUXT_API_BASE_URL): the in-network address compose
    // services reach (http://api:8080/api). Falls back to the public
    // base when unset.
    apiBaseUrl: '',
    public: {
      apiBaseUrl: '',
      wsUrl: '',
    },
  },
  app: {
    head: {
      htmlAttrs: { lang: 'en' },
      meta: [{ name: 'viewport', content: 'width=device-width, initial-scale=1' }],
    },
  },
})
