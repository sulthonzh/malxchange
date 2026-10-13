import { mount, RouterLinkStub, type VueWrapper } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { type Component, nextTick } from 'vue'
import type * as VueModule from 'vue'
import { vi } from 'vitest'

// Page SFCs rely on Nuxt auto-imports; under plain vitest those
// identifiers are ordinary globals. Each spec installs the real Vue
// exports for the reactive core and stubs exactly the Nuxt pieces its
// page touches, overriding the collaborators it observes.

const VUE_AUTO_IMPORTS = ['ref', 'reactive', 'computed', 'onMounted', 'onUnmounted'] as const

export interface NuxtOverrides {
  useApi?: () => unknown
  useSession?: () => unknown
  useForgotPassword?: () => unknown
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
    overrides.useApi ?? (() => ({ apiFetch: vi.fn(), apiBaseUrl: 'http://api.test' })),
  )
  vi.stubGlobal(
    'useSession',
    overrides.useSession ??
      (() => ({ setSession: vi.fn(), clearSession: vi.fn(), loggedIn: { value: true } })),
  )
  vi.stubGlobal(
    'useForgotPassword',
    overrides.useForgotPassword ?? (() => ({ requestCode: vi.fn(), resetPassword: vi.fn() })),
  )
  vi.stubGlobal('useRoute', overrides.useRoute ?? (() => ({ query: {} })))
  vi.stubGlobal('navigateTo', overrides.navigateTo ?? vi.fn())
  vi.stubGlobal('useHead', () => {})
  vi.stubGlobal('definePageMeta', () => {})
}

/** Mount a page with the shared ElementPlus chrome and NuxtLink stub. */
export function mountPage(component: Component, options: Record<string, unknown> = {}): VueWrapper {
  return mount(component, {
    attachTo: document.body,
    ...options,
    global: {
      plugins: [ElementPlus],
      stubs: { NuxtLink: RouterLinkStub },
      ...((options.global as Record<string, unknown> | undefined) ?? {}),
    },
  })
}

/** Wait for element-plus async validation and pending DOM updates. */
export async function settle(...wrappers: Array<{ $nextTick: () => Promise<void> }>): Promise<void> {
  await nextTick()
  for (const wrapper of wrappers) {
    await wrapper.$nextTick()
  }
  await nextTick()
}
