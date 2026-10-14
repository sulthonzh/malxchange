// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import WithdrawalsQueue from '../src/pages/finance/withdrawals/index.vue'

// The withdrawal audit queue (issue #59): member/state filters over
// /api/withdrawals, money as verbatim decimal strings, rows linking
// into the detail page.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const rows = [
  {
    id: 'w-1',
    coin: 'USDT',
    member: 'iris',
    address: '0xiris',
    amount: '20',
    fee: '2',
    net: '18',
    state: 'audit',
    createdAt: '2026-10-14 09:00:00+00',
  },
  {
    id: 'w-2',
    coin: 'ETH',
    member: 'jack',
    address: '0xjack',
    amount: '2',
    fee: '0.004',
    net: '1.996',
    state: 'failed',
    error: 'broadcast: chain unreachable',
    txid: '',
    createdAt: '2026-10-14 08:00:00+00',
  },
]

describe('withdrawals queue page', () => {
  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    collaborators.apiFetch.mockResolvedValue(rows)
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
    })
  })

  async function mountLoaded(): Promise<VueWrapper> {
    const wrapper = mountPage(WithdrawalsQueue)
    await flushPromises()
    await settle()
    return wrapper
  }

  it('loads the unfiltered queue on mount and renders both rows with a detail link', async () => {
    const wrapper = await mountLoaded()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/withdrawals')
    expect(wrapper.findAll('tbody tr')).toHaveLength(2)
    expect(wrapper.text()).toContain('iris')
    expect(wrapper.text()).toContain('0.004')
    const link = wrapper.find('tbody a')
    expect(link.attributes('href')).toBe('/finance/withdrawals/w-1')
  })

  it('sends member and state filters when searching', async () => {
    collaborators.apiFetch.mockResolvedValue([])
    const wrapper = await mountLoaded()
    collaborators.apiFetch.mockClear()

    await wrapper.find('input[name="member"]').setValue('iris')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/withdrawals?member=iris')
  })

  it('renders the api sentence when the queue fails to load', async () => {
    collaborators.apiFetch.mockRejectedValue(new Error('The wallet service is unreachable. Try again.'))
    const wrapper = mountPage(WithdrawalsQueue)
    await flushPromises()
    await settle()

    expect(wrapper.find('.error').text()).toContain('The wallet service is unreachable. Try again.')
  })

  it('renders an empty-state sentence instead of rows when nothing matches', async () => {
    collaborators.apiFetch.mockResolvedValue([])
    const wrapper = await mountLoaded()
    expect(wrapper.findAll('tbody tr')).toHaveLength(0)
    expect(wrapper.text()).toContain('No withdrawals match these filters.')
  })
})
