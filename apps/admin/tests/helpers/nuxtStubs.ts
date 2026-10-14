import ElementPlus from 'element-plus'
import { mount, type VueWrapper } from '@vue/test-utils'
import { type Component, nextTick } from 'vue'
import type * as VueModule from 'vue'
import { ref } from 'vue'
import { vi } from 'vitest'

// Page SFCs rely on Nuxt auto-imports; under plain vitest those
// identifiers are ordinary globals. Each spec installs the real Vue
// exports for the reactive core and stubs exactly the Nuxt pieces its
// page touches, overriding the collaborators it observes (apps/web's
// tests/helpers/nuxtStubs.ts pattern).

// NuxtLink stub: renders a real anchor with the destination as href,
// so specs assert where a link points without pulling in the Nuxt
// router (apps/web's tests/page-mount.ts pattern).
const NuxtLinkStub = {
  props: ['to'],
  template: '<a :href="String(to)"><slot /></a>',
}


const VUE_AUTO_IMPORTS = ['ref', 'reactive', 'computed', 'onMounted', 'onUnmounted'] as const

export interface SessionState {
  employee?: { id: string; email: string; role: string }
  mustRotate?: boolean
  totpBound?: boolean
  permissions?: string[]
}

export interface NuxtOverrides {
  useApi?: () => unknown
  useAdminSession?: () => unknown
  useRoute?: () => unknown
  navigateTo?: (to: string) => unknown
}

/** Install the free identifiers a page SFC expects, per test. */
export function stubNuxtGlobals(vue: VueModule, overrides: NuxtOverrides = {}): void {
  for (const name of VUE_AUTO_IMPORTS) {
    vi.stubGlobal(name, vue[name])
  }
  vi.stubGlobal(
    'useApi',
    overrides.useApi ?? (() => ({ apiFetch: vi.fn(), apiBaseUrl: 'http://admin-api.test' })),
  )
  // The session stub hands the spec a real ref it can read and write,
  // mirroring the composable's useState slot.
  vi.stubGlobal(
    'useAdminSession',
    overrides.useAdminSession ?? (() => ({ session: ref(null), loggedIn: computed(() => true) })),
  )
  vi.stubGlobal('useRoute', overrides.useRoute ?? (() => ({ query: {}, path: '/' })))
  vi.stubGlobal('navigateTo', overrides.navigateTo ?? vi.fn())
  vi.stubGlobal('useHead', () => {})
  vi.stubGlobal('definePageMeta', () => {})
}

/** A writable session ref seeded with `state` (null when omitted). */
export function sessionRef(state: SessionState | null = null) {
  return ref(state)
}

/** Mount a page with the shared ElementPlus chrome and NuxtLink stub. */
export function mountPage(component: Component, options: Record<string, unknown> = {}): VueWrapper {
  return mount(component, {
    attachTo: document.body,
    ...options,
    global: {
      plugins: [ElementPlus],
      stubs: { NuxtLink: NuxtLinkStub },
      ...((options.global as Record<string, unknown> | undefined) ?? {}),
    },
  })
}

/** Wait for element-plus async validation and pending DOM updates. */
/** Wait for element-plus async validation and pending DOM updates. */
export async function settle(): Promise<void> {
  await nextTick()
  await nextTick()
}
