// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import MemberDetailPage from '../src/pages/members/[id].vue'
import { ApiError } from '../src/composables/useApi'

// The member detail screen (issue #58): identity/status/KYC/Authenticator/
// Code-channel bindings, support's lock/unlock, and the Step-up-gated
// Authenticator reset (compliance; the server refuses non-verified KYC).

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const verified = {
  id: 'm-1',
  email: 'alice@example.com',
  status: 'active',
  phone: '+15551234567',
  kycStatus: 'verified',
  authenticatorBound: true,
  telegramBound: true,
  createdAt: '2026-01-01T00:00:00Z',
}

describe('member detail page', () => {
  let wrapper: VueWrapper

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch, apiBaseUrl: 'http://admin-api.test' }),
    })
  })

  it('shows identity, status, KYC state, Authenticator, and Code channels', async () => {
    collaborators.apiFetch.mockResolvedValue({ ...verified })
    wrapper = mountPage(MemberDetailPage, { props: { id: 'm-1' } })
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/members/m-1')
    expect(wrapper.find('[data-test="member-descriptions"]').text()).toContain('alice@example.com')
    expect(wrapper.find('[data-test="member-descriptions"]').text()).toContain('Bound')
    expect(wrapper.find('[data-test="member-channels"]').text()).toContain('SMS')
    expect(wrapper.find('[data-test="member-channels"]').text()).toContain('Telegram')
  })

  it('lists Email only when no phone or Telegram is bound', async () => {
    collaborators.apiFetch.mockResolvedValue({
      ...verified,
      phone: '',
      telegramBound: false,
    })
    wrapper = mountPage(MemberDetailPage, { props: { id: 'm-1' } })
    await flushPromises()
    await settle()

    expect(wrapper.find('[data-test="member-channels"]').text()).toContain('Email only')
  })

  it('locks an active member and reloads the view', async () => {
    collaborators.apiFetch
      .mockResolvedValueOnce({ ...verified })
      .mockResolvedValueOnce({ ...verified, status: 'locked' })
    wrapper = mountPage(MemberDetailPage, { props: { id: 'm-1' } })
    await flushPromises()

    await wrapper.find('[data-test="member-lock"]').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/members/m-1/lock', {
      method: 'POST',
      body: {},
    })
    expect(collaborators.apiFetch).toHaveBeenLastCalledWith('/api/members/m-1')
  })

  it('refuses the reset button for a member whose KYC is not verified', async () => {
    collaborators.apiFetch.mockResolvedValue({ ...verified, kycStatus: 'pending' })
    wrapper = mountPage(MemberDetailPage, { props: { id: 'm-1' } })
    await flushPromises()
    await settle()

    const reset = wrapper.find('[data-test="member-reset-open"]')
    expect((reset.element as HTMLButtonElement).disabled).toBe(true)
  })

  it('sends the Step-up code with the reset and closes on success', async () => {
    collaborators.apiFetch
      .mockResolvedValueOnce({ ...verified })
      .mockResolvedValueOnce({ ...verified, authenticatorBound: false })
    wrapper = mountPage(MemberDetailPage, { props: { id: 'm-1' } })
    await flushPromises()

    await wrapper.find('[data-test="member-reset-open"]').trigger('click')
    await settle()
    await wrapper.find('[data-test="member-reset-code"]').setValue('123456')
    await wrapper.find('[data-test="member-reset-confirm"]').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/members/m-1/totp/reset', {
      method: 'POST',
      body: { code: '123456' },
    })
  })

  it('surfaces the server refusal when api rejects the reset', async () => {
    collaborators.apiFetch
      .mockResolvedValueOnce({ ...verified })
      .mockRejectedValueOnce(
        new ApiError('authenticator reset requires a KYC-verified member', 409),
      )
    wrapper = mountPage(MemberDetailPage, { props: { id: 'm-1' } })
    await flushPromises()

    await wrapper.find('[data-test="member-reset-open"]').trigger('click')
    await settle()
    await wrapper.find('[data-test="member-reset-code"]').setValue('123456')
    await wrapper.find('[data-test="member-reset-confirm"]').trigger('click')
    await flushPromises()
    await settle()

    expect(wrapper.find('[data-test="member-reset-error"]').text()).toContain(
      'authenticator reset requires a KYC-verified member',
    )
  })

  it('renders the api sentence when the member cannot be loaded', async () => {
    collaborators.apiFetch.mockRejectedValue(new ApiError('member not found', 404))
    wrapper = mountPage(MemberDetailPage, { props: { id: 'ghost' } })
    await flushPromises()
    await settle()

    expect(wrapper.find('[data-test="member-detail-error"]').text()).toContain('member not found')
  })
})
