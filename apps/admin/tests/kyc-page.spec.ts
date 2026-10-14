// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import KycPage from '../src/pages/kyc/index.vue'
import { ApiError } from '../src/composables/useApi'

// The compliance KYC queue (issue #58): pending applications, document
// evidence fetched when the review opens, approve/reject with a
// reason — the server audit-logs every decision.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const application = {
  memberId: 'm-7',
  email: 'applicant@example.com',
  fullName: 'App Llicant',
  documentNumber: 'X-9',
  submittedAt: '2026-01-02T03:04:05Z',
}

const evidence = {
  memberId: 'm-7',
  fullName: 'App Llicant',
  documentNumber: 'X-9',
  frontImage: 'ZnJvbnQ=',
  backImage: 'YmFjaw==',
}

// jsdom has no ResizeObserver; element-plus tables observe their width.
class ResizeObserverStub {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

describe('kyc page', () => {
  let wrapper: VueWrapper

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch, apiBaseUrl: 'http://admin-api.test' }),
    })
    vi.stubGlobal('ResizeObserver', ResizeObserverStub)
  })

  it('loads the pending queue on entry', async () => {
    collaborators.apiFetch.mockResolvedValue({ total: 1, applications: [application] })
    wrapper = mountPage(KycPage)
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/kyc/applications?pageNo=1&pageSize=20')
    expect(wrapper.find('[data-test="kyc-table"]').text()).toContain('applicant@example.com')
    expect(wrapper.find('[data-test="kyc-table"]').text()).toContain('App Llicant')
  })

  it('fetches the evidence when the review opens and shows both images', async () => {
    collaborators.apiFetch
      .mockResolvedValueOnce({ total: 1, applications: [application] })
      .mockResolvedValueOnce(evidence)
    wrapper = mountPage(KycPage)
    await flushPromises()

    await wrapper.find('[data-test="kyc-review-open"]').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenLastCalledWith('/api/kyc/applications/m-7/evidence')
    const front = wrapper.find('[data-test="kyc-front"] img')
    const back = wrapper.find('[data-test="kyc-back"] img')
    expect(front.attributes('src')).toBe('data:image/jpeg;base64,ZnJvbnQ=')
    expect(back.attributes('src')).toBe('data:image/jpeg;base64,YmFjaw==')
  })

  it('refuses a rejection without a reason before calling admin-api', async () => {
    collaborators.apiFetch
      .mockResolvedValueOnce({ total: 1, applications: [application] })
      .mockResolvedValueOnce(evidence)
    wrapper = mountPage(KycPage)
    await flushPromises()
    await wrapper.find('[data-test="kyc-review-open"]').trigger('click')
    await flushPromises()
    await settle()

    collaborators.apiFetch.mockClear()
    await wrapper.find('[data-test="kyc-reject"]').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="kyc-review-error"]').text()).toContain(
      'A reason is required to reject a KYC application.',
    )
  })

  it('approves with the recorded reason and reloads the queue', async () => {
    collaborators.apiFetch
      .mockResolvedValueOnce({ total: 1, applications: [application] })
      .mockResolvedValueOnce(evidence)
      .mockResolvedValueOnce({ total: 0, applications: [] })
    wrapper = mountPage(KycPage)
    await flushPromises()
    await wrapper.find('[data-test="kyc-review-open"]').trigger('click')
    await flushPromises()
    await settle()

    await wrapper.find('[data-test="kyc-reason"]').setValue('documents match')
    await wrapper.find('[data-test="kyc-approve"]').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/kyc/applications/m-7/review', {
      method: 'POST',
      body: { approved: true, reason: 'documents match' },
    })
  })

  it('surfaces the server refusal when the application is no longer pending', async () => {
    collaborators.apiFetch
      .mockResolvedValueOnce({ total: 1, applications: [application] })
      .mockResolvedValueOnce(evidence)
      .mockRejectedValueOnce(new ApiError('no pending kyc application for this member', 409))
    wrapper = mountPage(KycPage)
    await flushPromises()
    await wrapper.find('[data-test="kyc-review-open"]').trigger('click')
    await flushPromises()
    await settle()

    await wrapper.find('[data-test="kyc-reason"]').setValue('already handled')
    await wrapper.find('[data-test="kyc-approve"]').trigger('click')
    await flushPromises()
    await settle()

    expect(wrapper.find('[data-test="kyc-review-error"]').text()).toContain(
      'no pending kyc application for this member',
    )
  })
})
