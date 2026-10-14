// @vitest-environment jsdom
import { flushPromises } from '@vue/test-utils'
import { computed } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import Reconcile from '../src/pages/operations/reconcile.vue'

// The reconcile view (issue #60): the fold invariant table and the
// per-Coin hot-wallet figures, with the ok/mismatch banner.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const reportPayload = {
  ok: true,
  fold: [
    {
      coin: 'BTC',
      ledgerAvail: '9',
      ledgerFrozen: '1',
      journalAvail: '9',
      journalFrozen: '1',
      ok: true,
    },
  ],
  coins: [
    {
      coin: 'BTC',
      ledgerSum: '10',
      feesCollected: '0.0004',
      coldOut: '0',
      coldIn: '0',
      inFlight: '0',
      unswept: '0',
      expectedHot: '10.0004',
      chainHot: '10.0004',
      delta: '0',
      ok: true,
    },
  ],
}

describe('reconcile page', () => {
  const session = sessionRef({
    employee: { id: 'e-1', email: 'finance@exchange.example.com', role: 'finance' },
    mustRotate: false,
    totpBound: true,
    permissions: ['view:reconcile'],
  })

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    session.value = {
      employee: { id: 'e-1', email: 'finance@exchange.example.com', role: 'finance' },
      mustRotate: false,
      totpBound: true,
      permissions: ['view:reconcile'],
    }
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch, apiBaseUrl: 'http://admin-api.test' }),
      useAdminSession: () => ({ session, loggedIn: computed(() => true) }),
    })
  })

  it('renders the fold invariant and the per-Coin hot figures', async () => {
    collaborators.apiFetch.mockResolvedValue(reportPayload)
    const wrapper = mountPage(Reconcile)
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/ops/reconcile')
    expect(wrapper.text()).toContain('Everything folds')
    const fold = wrapper.find('[data-test="fold-rows"]')
    expect(fold.text()).toContain('BTC')
    expect(fold.text()).toContain('9')
    const coins = wrapper.find('[data-test="coin-rows"]')
    expect(coins.text()).toContain('10.0004')
    expect(coins.text()).toContain('0.0004')
  })

  it('flags the mismatch when a Coin drifts', async () => {
    collaborators.apiFetch.mockResolvedValue({
      ...reportPayload,
      ok: false,
      coins: [{ ...reportPayload.coins[0], chainHot: '9.0004', delta: '1', ok: false }],
    })
    const wrapper = mountPage(Reconcile)
    await flushPromises()
    await settle()

    expect(wrapper.text()).toContain('Reconciliation found a mismatch')
    expect(wrapper.find('[data-test="coin-rows"]').text()).toContain('1')
  })

  it('renders the api sentence when the report fails', async () => {
    collaborators.apiFetch.mockRejectedValue(
      Object.assign(new Error('The wallet service is unreachable. Try again.'), { status: 503 }),
    )
    const wrapper = mountPage(Reconcile)
    await flushPromises()
    await settle()

    expect(wrapper.text()).toContain('The wallet service is unreachable. Try again.')
  })
})
