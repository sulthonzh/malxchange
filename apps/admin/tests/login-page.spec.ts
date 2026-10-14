// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import Login from '../src/pages/login.vue'

// The login page (issue #56): email + password + the authenticator code
// (mandatory once two-factor is bound — the one-time first-login
// credential needs none). A successful sign-in mirrors the wire
// response into the session state and lands the Employee by their
// forced-setup state; api failures render as sentences.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
  navigateTo: vi.fn(),
}))

describe('login page', () => {
  const session = sessionRef(null)

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    collaborators.navigateTo.mockReset()
    session.value = null
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
      useAdminSession: () => ({ session }),
      navigateTo: collaborators.navigateTo,
    })
  })

  async function fill(wrapper: VueWrapper, code = ''): Promise<void> {
    await wrapper.find('input[name="email"]').setValue('root@exchange.example.com')
    await wrapper.find('input[name="password"]').setValue('one-time-password')
    if (code) {
      await wrapper.find('input[name="code"]').setValue(code)
    }
  }

  it('signs in with email, password, and code, and lands a fresh credential on rotation', async () => {
    collaborators.apiFetch.mockResolvedValue({
      employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
      must_rotate: true,
      totp_bound: false,
      permissions: ['view:employee', 'view:role'],
    })
    const wrapper = mountPage(Login)
    await fill(wrapper, '123456')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/login', {
      method: 'POST',
      body: { email: 'root@exchange.example.com', password: 'one-time-password', code: '123456' },
    })
    expect(session.value).toEqual({
      employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
      mustRotate: true,
      totpBound: false,
      permissions: ['view:employee', 'view:role'],
    })
    expect(collaborators.navigateTo).toHaveBeenCalledWith('/setup/password')
    expect(wrapper.find('.auth-error').exists()).toBe(false)
  })

  it('lands a provisioned Employee on the home page', async () => {
    collaborators.apiFetch.mockResolvedValue({
      employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
      must_rotate: false,
      totp_bound: true,
      permissions: [],
    })
    const wrapper = mountPage(Login)
    await fill(wrapper, '654321')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(collaborators.navigateTo).toHaveBeenCalledWith('/')
  })

  it('renders the api sentence when sign-in fails and stores nothing', async () => {
    collaborators.apiFetch.mockRejectedValue(
      new Error('Email, password, or authenticator code is incorrect.'),
    )
    const wrapper = mountPage(Login)
    await fill(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(wrapper.find('.auth-error').text()).toContain(
      'Email, password, or authenticator code is incorrect.',
    )
    expect(session.value).toBeNull()
    expect(collaborators.navigateTo).not.toHaveBeenCalled()
  })
})
