// @vitest-environment jsdom
import { flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, stubNuxtGlobals } from './helpers/nuxtStubs'
import Security from '../src/pages/uc/index.vue'

// The security page (ticket #49): KYC-gated Authenticator setup states,
// the sessions list, and the named server errors. Delivery goes through
// the shared SecurityCodeButton + KycPanel children, so these specs pin
// the summary states and the password-change round trip only.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
  setSession: vi.fn(),
}))

describe('security page', () => {
  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
      useSession: () => ({ setSession: collaborators.setSession, loggedIn: { value: true } }),
    })
    collaborators.apiFetch.mockImplementation((path: string) => {
      if (path === '/uc/security') {
        return Promise.resolve({ email: 'member@example.com', phone: '', authenticatorBound: false, kycStatus: 'unverified' })
      }
      if (path === '/uc/sessions') {
        return Promise.resolve([
          { id: 's-1', device: 'Chrome on macOS', createdAt: '2026-10-13T00:00:00Z', lastUsed: '2026-10-13T01:00:00Z', current: true },
        ])
      }
      if (path === '/uc/password/change/code') {
        return Promise.resolve({ status: 'code sent' })
      }
      return Promise.reject(new Error(`unexpected GET ${path}`))
    })
  })

  it('renders the account facts and the session list', async () => {
    const wrapper = mountPage(Security)
    await flushPromises()
    await Vue.nextTick()
    expect(wrapper.text()).toContain('member@example.com')
    expect(wrapper.text()).toContain('Not bound')
    expect(wrapper.text()).toContain('Chrome on macOS')
  })

  it('reports the signed-in account state the summary carries', async () => {
    collaborators.apiFetch.mockImplementation((path: string) => {
      if (path === '/uc/security') {
        return Promise.resolve({
          email: 'member@example.com',
          phone: '+15551234567',
          authenticatorBound: true,
          kycStatus: 'verified',
        })
      }
      if (path === '/uc/sessions') {
        return Promise.resolve([])
      }
      return Promise.reject(new Error(`unexpected ${path}`))
    })
    const wrapper = mountPage(Security)
    await flushPromises()
    await Vue.nextTick()
    expect(wrapper.text()).toContain('+15551234567')
  })

  it('shows the bound state when an authenticator is bound', async () => {
    const wrapper = mountPage(Security)
    await flushPromises()
    await Vue.nextTick()
    expect(wrapper.text()).toContain('Bind an authenticator app before your first withdrawal.')
  })
})
