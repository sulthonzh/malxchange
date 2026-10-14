import vue from '@vitejs/plugin-vue'
import { fileURLToPath } from 'node:url'

import { defineConfig } from 'vitest/config'

// Node stays the default environment; DOM-dependent specs opt in with a
// per-file `// @vitest-environment` pragma (jsdom, matching apps/web).
// The vue plugin + `~` alias let the page SFC specs mount real
// components, which rely on Nuxt auto-imports — each spec installs the
// Vue exports and stubs the Nuxt pieces it observes.
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '~': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    include: ['tests/**/*.spec.ts'],
    environment: 'node',
  },
})
