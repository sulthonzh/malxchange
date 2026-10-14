// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import Home from '../src/pages/index.vue'
import AppSidebar from '../src/components/AppSidebar.vue'

// The home page and the permission-derived sidebar (issue #56): the
// shell renders the signed-in Employee and their live permission set;
// the sidebar is the static route table intersected with that set —
// presentation only, the server's permission middleware is the gate.

describe('home page', () => {
  it('greets the signed-in Employee with their role and live permissions', async () => {
    stubNuxtGlobals(Vue, {
      useAdminSession: () => ({
        session: sessionRef({
          employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
          mustRotate: false,
          totpBound: true,
          permissions: ['view:employee', 'view:role'],
        }),
      }),
    })
    const wrapper = mountPage(Home)
    await settle()

    expect(wrapper.text()).toContain('root@exchange.example.com')
    expect(wrapper.text()).toContain('super-admin')
    expect(wrapper.text()).toContain('view:employee')
    expect(wrapper.text()).toContain('view:role')
  })
})

describe('app sidebar', () => {
  function mountSidebar(permissions: string[]) {
    stubNuxtGlobals(Vue, {
      useAdminSession: () => ({
        session: sessionRef({
          employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
          mustRotate: false,
          totpBound: true,
          permissions,
        }),
      }),
    })
    return mountPage(AppSidebar)
  }

  it('offers Home plus every route the permission set admits', async () => {
    const wrapper = mountSidebar(['view:employee', 'view:role'])
    await settle()

    const hrefs = wrapper.findAll('a').map((a) => a.attributes('href'))
    expect(hrefs).toContain('/')
    expect(hrefs).toContain('/system/employees')
    expect(hrefs).toContain('/system/roles')
    expect(wrapper.text()).toContain('System')
  })

  it('hides routes outside the permission set — the sidebar is presentation only', async () => {
    const wrapper = mountSidebar([])
    await settle()

    const hrefs = wrapper.findAll('a').map((a) => a.attributes('href'))
    expect(hrefs).toContain('/')
    expect(hrefs).not.toContain('/system/employees')
    expect(hrefs).not.toContain('/system/roles')
    expect(wrapper.text()).not.toContain('System')
  })

  it('marks the current route active', async () => {
    stubNuxtGlobals(Vue, {
      useAdminSession: () => ({
        session: sessionRef({
          employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
          mustRotate: false,
          totpBound: true,
          permissions: ['view:employee'],
        }),
      }),
      useRoute: () => ({ path: '/system/employees' }),
    })
    const wrapper = mountPage(AppSidebar)
    await settle()

    const active = wrapper.find('a.active')
    expect(active.exists()).toBe(true)
    expect(active.attributes('href')).toBe('/system/employees')
  })
})
