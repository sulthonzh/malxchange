// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import WithdrawalDetail from '../src/pages/finance/withdrawals/[id].vue'

// The withdrawal detail (issue #59): full record, decisions gated by
// state — approve/reject from audit, retry/refund from failed — and
// the Step-up dialog carrying the fresh code on approve/retry.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const auditRow = {
  id: 'w-1',
  coin: 'USDT',
  member: 'iris',
  address: '0xiris',
  amount: '20',
  fee: '2',
  net: '18',
  state: 'audit',
  txid: '',
  error: '',
  createdAt: '2026-10-14 09:00:00+00',
  updatedAt: '2026-10-14 09:00:00+00',
}

describe('withdrawal detail page', () => {
  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    collaborators.apiFetch.mockResolvedValue({ ...auditRow })
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
    })
  })

  async function mountDetail(state = 'audit'): Promise<VueWrapper> {
    collaborators.apiFetch.mockImplementation((path: string) => {
      if (path === '/api/withdrawals/w-1') {
        return Promise.resolve({
          ...auditRow,
          state,
          error: state === 'failed' ? 'broadcast: chain unreachable' : '',
        })
      }
      return Promise.resolve({ ok: true })
    })
    const wrapper = mountPage(WithdrawalDetail, { props: { id: 'w-1' } })
    await flushPromises()
    await settle()
    return wrapper
  }

  it('loads the full record and shows both audit decisions', async () => {
    const wrapper = await mountDetail()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/withdrawals/w-1')
    expect(wrapper.text()).toContain('0xiris')
    expect(wrapper.text()).toContain('18')
    const buttons = wrapper.findAll('button').map((b) => b.text())
    expect(buttons).toContain('Approve')
    expect(buttons).toContain('Reject')
    expect(buttons).not.toContain('Retry broadcast')
    expect(buttons).not.toContain('Refund member')
  })

  it('refuses to Approve without entering a code', async () => {
    const wrapper = await mountDetail()
    collaborators.apiFetch.mockClear()

    await wrapper.findAll('button').find((b) => b.text() === 'Approve')!.trigger('click')
    await settle()
    // Dialog open, no code entered yet.
    await wrapper.findAll('button').find((b) => b.text() === 'Confirm')!.trigger('click')
    await settle()

    expect(collaborators.apiFetch).not.toHaveBeenCalled()
  })

  it('posts the fresh code with the approve action and reloads', async () => {
    const wrapper = await mountDetail()
    collaborators.apiFetch.mockClear()

    await wrapper.findAll('button').find((b) => b.text() === 'Approve')!.trigger('click')
    await settle()
    const codeInput = wrapper.find('input[name="code"]')
    await codeInput.setValue('123456')
    await wrapper.findAll('button').find((b) => b.text() === 'Confirm')!.trigger('click')
    await flushPromises()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/withdrawals/w-1/approve', {
      method: 'POST',
      body: { code: '123456' },
    })
    // The reload after success.
    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/withdrawals/w-1')
  })

  it('rejects immediately — reject carries no code — and reloads', async () => {
    const wrapper = await mountDetail()
    collaborators.apiFetch.mockClear()

    await wrapper.findAll('button').find((b) => b.text() === 'Reject')!.trigger('click')
    await flushPromises()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/withdrawals/w-1/reject', {
      method: 'POST',
      body: {},
    })
  })

  it('offers retry and refund only for a failed withdrawal', async () => {
    const wrapper = await mountDetail('failed')

    const buttons = wrapper.findAll('button').map((b) => b.text())
    expect(buttons).not.toContain('Approve')
    expect(buttons).not.toContain('Reject')
    expect(buttons).toContain('Retry broadcast')
    expect(buttons).toContain('Refund member')
    expect(wrapper.text()).toContain('broadcast: chain unreachable')
  })

  it('renders the api sentence when a decision fails', async () => {
    const wrapper = await mountDetail()
    collaborators.apiFetch.mockImplementation((path: string) => {
      if (path === '/api/withdrawals/w-1/reject') {
        return Promise.reject(new Error('This withdrawal is not in the audit state.'))
      }
      if (path === '/api/withdrawals/w-1') {
        return Promise.resolve({ ...auditRow, state: 'rejected' })
      }
      return Promise.resolve({ ok: true })
    })

    await wrapper.findAll('button').find((b) => b.text() === 'Reject')!.trigger('click')
    await flushPromises()
    await settle()

    expect(wrapper.find('.error').text()).toContain('This withdrawal is not in the audit state.')
  })
})
