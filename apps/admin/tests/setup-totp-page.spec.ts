// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import SetupTotp from '../src/pages/setup/totp.vue'

// The forced TOTP-enrollment screen (issue #56): enroll on entry (the
// secret is shown exactly once), confirm the first code, and the
// restricted session unlocks into the shell — same session, no second
// login (internal/admin/setup.go).

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
  navigateTo: vi.fn(),
}))

describe('setup totp page', () => {
  const session = sessionRef({
    employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
    mustRotate: false,
    totpBound: false,
    permissions: ['view:employee'],
  })

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    collaborators.navigateTo.mockReset()
    session.value = {
      employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
      mustRotate: false,
      totpBound: false,
      permissions: ['view:employee'],
    }
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
      useAdminSession: () => ({ session }),
      navigateTo: collaborators.navigateTo,
    })
  })

  it('enrolls on entry and shows the secret and otpauth URI exactly once', async () => {
    collaborators.apiFetch.mockResolvedValue({
      secret: 'JBSWY3DPEHPK3PXP',
      otpauth_uri: 'otpauth://totp/malxchange:root%40exchange.example.com?secret=JBSWY3DPEHPK3PXP',
    })
    const wrapper = mountPage(SetupTotp)
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/totp/enroll', { method: 'POST', body: {} })
    expect(wrapper.text()).toContain('JBSWY3DPEHPK3PXP')
    expect(wrapper.text()).toContain('otpauth://totp/malxchange:root%40exchange.example.com')
  })

  it('confirms the first code, unlocks the session, and lands home', async () => {
    collaborators.apiFetch.mockResolvedValueOnce({
      secret: 'JBSWY3DPEHPK3PXP',
      otpauth_uri: 'otpauth://totp/x?secret=JBSWY3DPEHPK3PXP',
    })
    collaborators.apiFetch.mockResolvedValueOnce({ totp_bound: true, must_rotate: false })
    const wrapper = mountPage(SetupTotp)
    await flushPromises()
    await settle()

    await wrapper.find('input[name="code"]').setValue('123456')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/totp/confirm', {
      method: 'POST',
      body: { code: '123456' },
    })
    expect(session.value?.totpBound).toBe(true)
    expect(collaborators.navigateTo).toHaveBeenCalledWith('/')
  })

  it('renders the api sentence when the code is rejected', async () => {
    collaborators.apiFetch.mockResolvedValueOnce({
      secret: 'JBSWY3DPEHPK3PXP',
      otpauth_uri: 'otpauth://totp/x?secret=JBSWY3DPEHPK3PXP',
    })
    collaborators.apiFetch.mockRejectedValueOnce(
      new Error('That code is not valid. Check your authenticator app and try again.'),
    )
    const wrapper = mountPage(SetupTotp)
    await flushPromises()
    await settle()

    await wrapper.find('input[name="code"]').setValue('000000')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(wrapper.find('.auth-error').text()).toContain(
      'That code is not valid. Check your authenticator app and try again.',
    )
    expect(session.value?.totpBound).toBe(false)
    expect(collaborators.navigateTo).not.toHaveBeenCalled()
  })
})
