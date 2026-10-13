// Runtime host configuration (spec #24, story 33): every host this app
// talks to arrives through runtimeConfig, fed by environment variables
// (NUXT_PUBLIC_API_BASE_URL, NUXT_PUBLIC_WS_URL — see .env.example).
// Nothing is baked into source; the legacy front's hardcoded
// Vue.prototype.host / rootHost literals die here. Defaults stay empty so
// an unconfigured app fails loudly at the api client instead of silently
// calling some baked-in host.
export default defineNuxtConfig({
  srcDir: 'src',
  ssr: true,
  modules: ['@element-plus/nuxt'],
  css: ['~/assets/scss/main.scss'],
  runtimeConfig: {
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
