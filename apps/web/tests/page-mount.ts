import ElementPlus from 'element-plus'
import { mount } from '@vue/test-utils'
import type { Component } from 'vue'

// NuxtLink stub: renders a real anchor with the destination as href, so
// specs assert where a link points without pulling in the Nuxt router.
const NuxtLinkStub = {
  props: ['to'],
  template: '<a :href="String(to)"><slot /></a>',
}

// useHead is a Nuxt auto-import; pages only set document titles with it,
// so the component tests neutralize it globally. The cast widens the
// global object by the auto-import slot — no narrower typed handle
// exists for "globalThis plus the stub".
const globalScope = globalThis as { useHead?: unknown; definePageMeta?: unknown }
globalScope.useHead = () => {}
// Pages that take route params declare `definePageMeta({ props: true })`
// (Nuxt compiles it away; the vitest node environment needs the stub).
globalScope.definePageMeta = () => {}

// Common mount for page SFCs: real element-plus (pagination etc.), the
// NuxtLink stub, and page props passed directly (the pages declare route
// params as props, so no router is involved).
export function mountPage(component: Component, props: Record<string, unknown> = {}) {
  return mount(component, {
    props,
    global: {
      plugins: [ElementPlus],
      stubs: { NuxtLink: NuxtLinkStub },
    },
  })
}
