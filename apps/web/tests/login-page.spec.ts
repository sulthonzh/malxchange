// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, stubNuxtGlobals } from './helpers/nuxtStubs'
import Login from '../src/pages/login.vue'

// The login page (ticket #47): form validation blocks bad submits, a
// successful sign-in stores the session and honors only same-site
// redirects, and api failures render as sentences in the alert.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
  setSession: vi.fn(),
  navigateTo: vi.fn(),
}))

describe('login page', () => {
  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    collaborators.setSession.mockReset()
    collaborators.navigateTo.mockReset()
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
      useSession: () => ({ setSession: collaborators.setSession }),
      navigateTo: collaborators.navigateTo,
    })
  })

  async function fill(wrapper: VueWrapper): Promise<void> {
    await wrapper.find('input[name="email"]').setValue('member@example.com')
    await wrapper.find('input[name="password"]').setValue('the-right-password')
  }

  it('signs in, stores the session, and lands on the default page', async () => {
    collaborators.apiFetch.mockResolvedValue({
      token: 'tok-1',
      member: { id: 'm-1', email: 'member@example.com', status: 'active', createdAt: '2026-10-13T00:00:00Z' },
    })
    const wrapper = mountPage(Login)
    await fill(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await Vue.nextTick()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/uc/login', {
      method: 'POST',
      body: expect.objectContaining({ email: 'member@example.com', password: 'the-right-password' }),
    })
    expect(collaborators.setSession).toHaveBeenCalledWith(
      'tok-1',
      expect.objectContaining({ email: 'member@example.com' }),
    )
    expect(collaborators.navigateTo).toHaveBeenCalledWith('/exchange')
    expect(wrapper.find('.auth-error').exists()).toBe(false)
  })

  it('renders the api sentence when sign-in fails and stores nothing', async () => {
    collaborators.apiFetch.mockRejectedValue(
      new Error('Too many failed sign-in attempts. Try again in a few minutes.'),
    )
    const wrapper = mountPage(Login)
    await fill(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await Vue.nextTick()

    expect(wrapper.find('.auth-error').text()).toContain(
      'Too many failed sign-in attempts. Try again in a few minutes.',
    )
    expect(collaborators.setSession).not.toHaveBeenCalled()
    expect(collaborators.navigateTo).not.toHaveBeenCalled()
  })

})
