import vue from '@vitejs/plugin-vue'
import { fileURLToPath } from 'node:url'

import { defineConfig } from 'vitest/config'

// Node stays the default environment; DOM-dependent specs opt in with a
// per-file `// @vitest-environment` pragma (jsdom — see
// tests/sanitize.spec.ts for why jsdom and not happy-dom: happy-dom's
// node iteration breaks DOMPurify). The vue plugin + `~` alias let the
// page SFC specs mount real components, which import their composables
// explicitly (~/composables/...) so vi.mock can intercept them.
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
