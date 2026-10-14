// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import SetupPassword from '../src/pages/setup/password.vue'

// The forced password-rotation screen (issue #56): current + new +
// confirm, POST /api/password. Rotation revokes every session — the
// api clears the cookie — so success drops the local mirror and lands
// on the login page for the second sign-in.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
  navigateTo: vi.fn(),
}))

describe('setup password page', () => {
  const session = sessionRef({
    employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
    mustRotate: true,
    totpBound: false,
    permissions: [],
  })

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    collaborators.navigateTo.mockReset()
    session.value = {
      employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
      mustRotate: true,
      totpBound: false,
      permissions: [],
    }
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
      useAdminSession: () => ({ session }),
      navigateTo: collaborators.navigateTo,
    })
  })

  async function fill(wrapper: VueWrapper): Promise<void> {
    await wrapper.find('input[name="current_password"]').setValue('one-time-password')
    await wrapper.find('input[name="new_password"]').setValue('rotated-secret-1')
    await wrapper.find('input[name="confirm_password"]').setValue('rotated-secret-1')
  }

  it('rotates, drops the revoked session, and returns to the login page', async () => {
    collaborators.apiFetch.mockResolvedValue({
      message: 'Password changed. Sign in with your new password.',
    })
    const wrapper = mountPage(SetupPassword)
    await fill(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/password', {
      method: 'POST',
      body: { current_password: 'one-time-password', new_password: 'rotated-secret-1' },
    })
    expect(session.value).toBeNull()
    expect(collaborators.navigateTo).toHaveBeenCalledWith('/login?rotated=1')
  })

  it('blocks a mismatched confirmation without calling the api', async () => {
    const wrapper = mountPage(SetupPassword)
    await wrapper.find('input[name="current_password"]').setValue('one-time-password')
    await wrapper.find('input[name="new_password"]').setValue('rotated-secret-1')
    await wrapper.find('input[name="confirm_password"]').setValue('different-secret')
    await wrapper.find('form').trigger('submit')
    await settle()

    expect(collaborators.apiFetch).not.toHaveBeenCalled()
    expect(collaborators.navigateTo).not.toHaveBeenCalled()
    expect(session.value).not.toBeNull()
  })

  it('renders the api sentence and keeps the session when rotation fails', async () => {
    collaborators.apiFetch.mockRejectedValue(new Error('Your current password is incorrect.'))
    const wrapper = mountPage(SetupPassword)
    await fill(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(wrapper.find('.auth-error').text()).toContain('Your current password is incorrect.')
    expect(session.value).not.toBeNull()
    expect(collaborators.navigateTo).not.toHaveBeenCalled()
  })
})
